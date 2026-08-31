package keypolicy

import (
	"context"
	"time"

	"cpa-usage-keeper/internal/cpa"
	"cpa-usage-keeper/internal/entities"
	"cpa-usage-keeper/internal/pricing"
	"cpa-usage-keeper/internal/repository"

	"github.com/sirupsen/logrus"
	"gorm.io/gorm"
)

// skippedLogDedupWindow 控制 skipped_last_key 审计的写入频率，避免每分钟刷屏。
const skippedLogDedupWindow = 10 * time.Minute

// Runner 周期性评估启用策略，并把 CPA key 状态收敛到目标态。
type Runner struct {
	db       *gorm.DB
	client   *cpa.Client
	store    *Store
	interval time.Duration
	logger   logrus.FieldLogger
	now      func() time.Time
	// wake 是容量 1 的缓冲 channel，把任意多次唤醒合并成一个待处理信号。
	wake chan struct{}
}

// NewRunner 构造执行器；interval 同时是兜底 tick 周期。
func NewRunner(db *gorm.DB, client *cpa.Client, catalog *pricing.Catalog, interval time.Duration, logger logrus.FieldLogger) *Runner {
	if interval <= 0 {
		interval = time.Minute
	}
	if logger == nil {
		logger = logrus.StandardLogger()
	}
	return &Runner{
		db: db, client: client, store: NewStore(db, catalog),
		interval: interval, logger: logger.WithField("runner", "keypolicy"),
		now: time.Now, wake: make(chan struct{}, 1),
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

// Run 阻塞运行：先立即评估一次，之后按唤醒或 interval 推进。
func (r *Runner) Run(ctx context.Context) error {
	ticker := time.NewTicker(r.interval)
	defer ticker.Stop()
	r.evaluateSafe(ctx)
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-r.wake:
			r.evaluateSafe(ctx)
		case <-ticker.C:
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
func (r *Runner) EvaluateOnce(ctx context.Context) error {
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
	for _, row := range rows {
		r.reconcileRow(ctx, row, usage[row.CPAAPIKeyID], present, len(currentKeys.Payload.APIKeys), daily, monthly, now)
	}
	return nil
}

// reconcileRow 把单条策略收敛到目标态，所有分支独立容错。
func (r *Runner) reconcileRow(ctx context.Context, row repository.CPAAPIKeyPolicyRow, usage UsageByWindow, present map[string]bool, cpaKeyCount int, daily, monthly Window, now time.Time) {
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
			// 已经禁用，只刷新评估时间。
			_ = repository.UpdateCPAAPIKeyPolicyRuntime(r.db, row.CPAAPIKeyID, string(state), row.DisabledWindowKey, now)
			return
		}
		// 最后一个 key 永不自动禁用。
		if cpaKeyCount <= 1 {
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
		if present[row.APIKey] {
			if _, err := r.client.DeleteManagementAPIKey(ctx, row.APIKey); err != nil {
				r.logger.WithError(err).WithField("cpa_api_key_id", row.CPAAPIKeyID).Warn("disable api key in cpa failed")
				// 状态回滚为 active：key 仍留在 CPA，下一轮评估会再次尝试删除并重新落禁用状态。
				if revertErr := repository.UpdateCPAAPIKeyPolicyRuntime(r.db, row.CPAAPIKeyID, string(StateActive), row.DisabledWindowKey, now); revertErr != nil {
					r.logger.WithError(revertErr).WithField("cpa_api_key_id", row.CPAAPIKeyID).Warn("revert api key policy state failed")
				}
				r.writeLog(row.CPAAPIKeyID, "failed", "retry", breach, err.Error())
				return
			}
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
// PUT 成功但随后的状态更新失败时，下一轮仍处于 disabled_by_quota，会再次进入这里并直接跳过 PUT。
func (r *Runner) restoreKey(ctx context.Context, row repository.CPAAPIKeyPolicyRow, daily, monthly Window, now time.Time) error {
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
