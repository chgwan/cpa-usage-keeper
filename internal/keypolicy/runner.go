package keypolicy

import (
	"context"
	"sync"
	"time"

	"cpa-usage-keeper/internal/cpa"
	"cpa-usage-keeper/internal/entities"
	"cpa-usage-keeper/internal/pricing"
	"cpa-usage-keeper/internal/repository"

	"github.com/sirupsen/logrus"
	"gorm.io/gorm"
)

const (
	// skippedLogDedupWindow 控制 skipped_last_key 审计的写入频率，避免每分钟刷屏。
	skippedLogDedupWindow = 10 * time.Minute
	// keyPolicyDebounceInterval 合并连续 usage 提交唤醒，窗口内后续唤醒不重置计时。
	keyPolicyDebounceInterval = 5 * time.Second
)

// Runner 周期性评估启用策略，并把 CPA key 状态收敛到目标态。
type Runner struct {
	db       *gorm.DB
	client   *cpa.Client
	store    *Store
	interval time.Duration
	logger   logrus.FieldLogger
	now      func() time.Time
	// keyMutations 与管理服务共享同一把 key 变更互斥锁，串行化双方的全量列表 PUT。
	keyMutations sync.Locker
	// evalMu 保证同一时刻只有一轮评估在收敛 CPA 状态。
	evalMu sync.Mutex
	// wake 是容量 1 的缓冲 channel，把任意多次唤醒合并成一个待处理信号。
	wake chan struct{}
	// debounceInterval 是唤醒后等待静默的 one-shot 窗口长度。
	debounceInterval time.Duration
}

// NewRunner 构造执行器；interval 同时是兜底 tick 周期。
// keyMutations 必须与管理服务注入同一实例，nil 时退化为独享锁（仅测试使用）。
func NewRunner(db *gorm.DB, client *cpa.Client, catalog *pricing.Catalog, interval time.Duration, logger logrus.FieldLogger, keyMutations sync.Locker) *Runner {
	if interval <= 0 {
		interval = time.Minute
	}
	if logger == nil {
		logger = logrus.StandardLogger()
	}
	if keyMutations == nil {
		keyMutations = &sync.Mutex{}
	}
	return &Runner{
		db: db, client: client, store: NewStore(db, catalog),
		interval: interval, logger: logger.WithField("runner", "keypolicy"),
		now: time.Now, wake: make(chan struct{}, 1),
		keyMutations: keyMutations, debounceInterval: keyPolicyDebounceInterval,
	}
}

// NotifyUsageEventsCommitted 非阻塞接收 usage 提交信号并唤醒评估。
func (r *Runner) NotifyUsageEventsCommitted([]entities.UsageEvent) { r.wakeNow() }

// NotifyUsageIdentitiesChanged 同样只做非阻塞唤醒。
func (r *Runner) NotifyUsageIdentitiesChanged() { r.wakeNow() }

// wakeNow 用非阻塞发送保证重复唤醒合并为一次；channel 已满时直接放弃。
func (r *Runner) wakeNow() {
	select {
	case r.wake <- struct{}{}:
	default:
	}
}

// Run 阻塞运行：先立即评估一次自愈历史状态；之后唤醒只进入 debounce 静默窗口，
// 窗口到期（复用 usage aggregation runner 的 arm/不 reset 语义）或兜底 ticker 触发评估。
func (r *Runner) Run(ctx context.Context) error {
	ticker := time.NewTicker(r.interval)
	defer ticker.Stop()
	r.evaluateSafe(ctx)
	var debounce *time.Timer
	for {
		if debounce == nil {
			select {
			case <-ctx.Done():
				return nil
			case <-r.wake:
				// 第一个唤醒进入静默窗口，窗口内的后续唤醒被容量 1 的 channel 自然吸收。
				debounce = time.NewTimer(r.debounceInterval)
			case <-ticker.C:
				r.evaluateSafe(ctx)
			}
			continue
		}
		select {
		case <-ctx.Done():
			debounce.Stop()
			return nil
		case <-debounce.C:
			debounce = nil
			r.evaluateSafe(ctx)
		case <-r.wake:
			// 已有待处理评估：吸收唤醒但不重置计时，保证最迟 5 秒必然执行。
		case <-ticker.C:
			// 兜底 tick 已经评估过，取消未触发的 debounce 避免紧接着二次评估。
			debounce.Stop()
			debounce = nil
			r.evaluateSafe(ctx)
		}
	}
}

// evaluateSafe 包装评估错误，runner 永不因单次失败退出。
func (r *Runner) evaluateSafe(ctx context.Context) {
	if err := r.EvaluateOnce(ctx); err != nil {
		r.logger.WithError(err).Warn("evaluate api key policies failed")
	}
}

// shouldWriteSkippedLog 判断 skipped 审计是否已滑出去重窗口。
func (r *Runner) shouldWriteSkippedLog(cpaAPIKeyID int64, now time.Time) bool {
	latest, err := repository.LatestAPIKeyEnforcementLogAction(r.db, cpaAPIKeyID, "skipped_last_key")
	if err != nil {
		// 没有历史记录或查询失败都允许写入。
		return true
	}
	return now.Sub(latest.CreatedAt) >= skippedLogDedupWindow
}

// EvaluateOnce 单轮评估：查询用量 → 判定 → 与 CPA 实际状态收敛。
// evalMu 串行化整轮收敛，防止并发调用者交错出两次 DELETE / 两次全量 PUT。
func (r *Runner) EvaluateOnce(ctx context.Context) error {
	r.evalMu.Lock()
	defer r.evalMu.Unlock()
	rows, err := repository.ListEnabledCPAAPIKeyPolicies(r.db)
	if err != nil {
		return err
	}
	now := r.now()
	daily, monthly := DailyWindow(now), MonthlyWindow(now)
	usage, err := r.store.PerKeyUsage(ctx, daily, monthly)
	if err != nil {
		return err
	}
	currentKeys, err := r.client.FetchManagementAPIKeys(ctx)
	if err != nil {
		// CPA 不可达时保持现状，下一轮重试。
		return err
	}
	present := make(map[string]bool, len(currentKeys.Payload.APIKeys))
	for _, key := range currentKeys.Payload.APIKeys {
		present[key] = true
	}
	round := &roundState{remaining: len(currentKeys.Payload.APIKeys), present: present}
	for _, row := range rows {
		r.reconcileRow(ctx, row, usage[row.CPAAPIKeyID], round, daily, monthly, now)
	}
	return nil
}

// roundState 是单轮评估共享的可变 CPA 视图。每条策略看到的 key 数量必须反映本轮
// 已发生的删除：若各分支沿用轮首快照，CPA=[A,B] 双双超限时 A 删除成功后 B 仍按
// 旧计数 2 放行，最后一个 key 会被删空，所有客户端立即断连。
type roundState struct {
	// remaining 是 CPA 当前 key 数量；收缩到 1 时任何自动 DELETE 都必须跳过。
	remaining int
	// present 记录 key 是否仍在 CPA 服务里，DELETE 成功后立即移除。
	present map[string]bool
}

// shrink 只在 DELETE 成功后收缩轮内视图；失败路径绝不调用，下一轮以全新 GET 重建真相。
func (s *roundState) shrink(apiKey string) {
	delete(s.present, apiKey)
	s.remaining--
}

// reconcileRow 把单条策略收敛到目标态，所有分支独立容错。
func (r *Runner) reconcileRow(ctx context.Context, row repository.CPAAPIKeyPolicyRow, usage UsageByWindow, round *roundState, daily, monthly Window, now time.Time) {
	limits, err := ParseLimits(row.Limits)
	if err != nil {
		r.logger.WithError(err).WithField("cpa_api_key_id", row.CPAAPIKeyID).Warn("parse api key limits failed")
		return
	}
	state := EnforcementState(row.EnforcementState)
	// 手动禁用完全交给管理员，runner 不碰。
	if row.AdminDisabled || state == StateDisabledManual {
		return
	}
	breach := Evaluate(limits, usage)
	if breach != nil {
		windowKey := WindowKey(daily)
		if breach.Limit.Window == LimitWindowMonthly {
			windowKey = WindowKey(monthly)
		}
		if state == StateDisabledByQuota {
			// 状态已禁用但 key 可能仍在 CPA 服务：禁用流程在状态写成功后、DELETE 完成前崩溃，
			// 或 DELETE 失败且状态回滚写也失败，都会停留在这个格点。每轮对在场 key 幂等重发
			// DELETE 收敛——成功不写状态不刷审计，失败才落 failed/retry 等下一轮重试。
			if round.present[row.APIKey] {
				// 补发 DELETE 同样受 last-key 守卫：残留 key 已是 CPA 最后一个时不能删空。
				if round.remaining <= 1 {
					if r.shouldWriteSkippedLog(row.CPAAPIKeyID, now) {
						r.writeLog(row.CPAAPIKeyID, "skipped_last_key", "limit_breached", breach, "last remaining api key")
					}
					_ = repository.UpdateCPAAPIKeyPolicyRuntime(r.db, row.CPAAPIKeyID, string(state), row.DisabledWindowKey, now)
					return
				}
				if _, err := r.client.DeleteManagementAPIKey(ctx, row.APIKey); err != nil {
					r.logger.WithError(err).WithField("cpa_api_key_id", row.CPAAPIKeyID).Warn("re-disable api key in cpa failed")
					r.writeLog(row.CPAAPIKeyID, "failed", "retry", breach, err.Error())
					return
				}
				round.shrink(row.APIKey)
			}
			_ = repository.UpdateCPAAPIKeyPolicyRuntime(r.db, row.CPAAPIKeyID, string(state), row.DisabledWindowKey, now)
			return
		}
		// 最后一个 key 永不自动禁用；计数取轮内实时值，本轮早先的删除已经收缩。
		if round.remaining <= 1 {
			if r.shouldWriteSkippedLog(row.CPAAPIKeyID, now) {
				r.writeLog(row.CPAAPIKeyID, "skipped_last_key", "limit_breached", breach, "last remaining api key")
			}
			_ = repository.UpdateCPAAPIKeyPolicyRuntime(r.db, row.CPAAPIKeyID, string(state), row.DisabledWindowKey, now)
			return
		}
		// TOCTOU：周期 metadata sync 不持有管理服务的互斥锁。若先删 CPA 再写状态，两次写之间的
		// sync tick 会因为状态仍是 active 把本地行软删除。因此先把状态写成 disabled_by_quota
		//（sync 的策略保护立即生效，窗口被完全关闭），再向 CPA 发 DELETE；
		// DELETE 失败则把状态回滚为 active，让下一轮重新走完整禁用流程并重试删除。
		if err := repository.UpdateCPAAPIKeyPolicyRuntime(r.db, row.CPAAPIKeyID, string(StateDisabledByQuota), windowKey, now); err != nil {
			r.logger.WithError(err).WithField("cpa_api_key_id", row.CPAAPIKeyID).Warn("update api key policy state failed")
			return
		}
		if round.present[row.APIKey] {
			if _, err := r.client.DeleteManagementAPIKey(ctx, row.APIKey); err != nil {
				r.logger.WithError(err).WithField("cpa_api_key_id", row.CPAAPIKeyID).Warn("disable api key in cpa failed")
				// 状态回滚为 active：key 仍留在 CPA，下一轮评估会再次尝试删除并重新落禁用状态。
				if revertErr := repository.UpdateCPAAPIKeyPolicyRuntime(r.db, row.CPAAPIKeyID, string(StateActive), row.DisabledWindowKey, now); revertErr != nil {
					r.logger.WithError(revertErr).WithField("cpa_api_key_id", row.CPAAPIKeyID).Warn("revert api key policy state failed")
				}
				r.writeLog(row.CPAAPIKeyID, "failed", "retry", breach, err.Error())
				return
			}
			round.shrink(row.APIKey)
		}
		r.writeLog(row.CPAAPIKeyID, "disabled", "limit_breached", breach, "")
		return
	}
	// 未超限但处于超限禁用态：自动恢复。必须先 re-add 再改状态——
	// 状态一旦先变 active，sync 的策略保护立即失效，下一个 sync tick 会在 key 回到 CPA 前软删本地行。
	if state == StateDisabledByQuota {
		if err := r.restoreKey(ctx, row, daily, monthly, now); err != nil {
			r.logger.WithError(err).WithField("cpa_api_key_id", row.CPAAPIKeyID).Warn("restore api key in cpa failed")
			r.writeLog(row.CPAAPIKeyID, "failed", "retry", nil, err.Error())
			return
		}
	}
	_ = repository.UpdateCPAAPIKeyPolicyRuntime(r.db, row.CPAAPIKeyID, string(StateActive), "", now)
}

// restoreKey 用 GET→追加→PUT 把 key 加回 CPA；key 已在 CPA 时幂等跳过。
// 全量 PUT 与管理服务的 Create/Restore 共享同一把 keyMutations 锁：GET 与 PUT 必须原子，
// 否则与管理员的 GET→追加→PUT 交错会静默丢弃对方刚写入的 key。
// PUT 成功但随后的状态更新失败时，下一轮仍处于 disabled_by_quota，会再次进入这里并直接跳过 PUT。
func (r *Runner) restoreKey(ctx context.Context, row repository.CPAAPIKeyPolicyRow, daily, monthly Window, now time.Time) error {
	r.keyMutations.Lock()
	defer r.keyMutations.Unlock()
	current, err := r.client.FetchManagementAPIKeys(ctx)
	if err != nil {
		return err
	}
	for _, existing := range current.Payload.APIKeys {
		if existing == row.APIKey {
			// 已在 CPA 中，只补状态。
			return nil
		}
	}
	if _, err := r.client.ReplaceManagementAPIKeys(ctx, append(current.Payload.APIKeys, row.APIKey)); err != nil {
		return err
	}
	reason := "policy_updated"
	if row.DisabledWindowKey != "" && row.DisabledWindowKey != WindowKey(daily) && row.DisabledWindowKey != WindowKey(monthly) {
		reason = "window_reset"
	}
	r.writeLog(row.CPAAPIKeyID, "restored", reason, nil, "")
	return nil
}

// writeLog 落审计，nil breach 表示与限额无关的动作。
func (r *Runner) writeLog(cpaAPIKeyID int64, action, reason string, breach *Breach, detail string) {
	entry := entities.APIKeyEnforcementLog{
		CPAAPIKeyID: cpaAPIKeyID, Action: action, Reason: reason, Detail: detail, CreatedAt: r.now(),
	}
	if breach != nil {
		limitType := string(breach.Limit.Type)
		window := string(breach.Limit.Window)
		entry.LimitType = &limitType
		entry.Window = &window
		entry.UsedValue = &breach.Used
		entry.LimitValue = &breach.Limit.Value
	}
	if err := repository.InsertAPIKeyEnforcementLog(r.db, entry); err != nil {
		r.logger.WithError(err).Warn("write api key enforcement log failed")
	}
}
