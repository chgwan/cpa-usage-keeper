package service

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"slices"
	"strings"
	"sync"
	"time"

	"cpa-usage-keeper/internal/cpa"
	"cpa-usage-keeper/internal/entities"
	"cpa-usage-keeper/internal/keypolicy"
	"cpa-usage-keeper/internal/pricing"
	"cpa-usage-keeper/internal/repository"

	"github.com/sirupsen/logrus"
	"gorm.io/gorm"
)

// ErrCPARequestFailed 标记 CPA 管理接口写失败，路由层映射为 502。
var ErrCPARequestFailed = errors.New("cpa request failed")

// ErrKeyDisabled 标记 key 当前处于禁用态，重新生成等操作被拒，路由层映射为 409。
var ErrKeyDisabled = errors.New("api key is disabled")

// ErrInvalidInput 标记请求参数校验失败（自定义 key 非法、key 重复、限额非法），路由层映射为 400。
var ErrInvalidInput = errors.New("invalid input")

// quotaAdjustmentMaxAbsDeltaUSD 是临时调整额 delta 的绝对值上限：防止 limit+delta 溢出到
// +Inf 把响应 JSON 编码搞坏，前端 buildQuotaAdjustmentRequest 用同一个值做镜像校验。
const quotaAdjustmentMaxAbsDeltaUSD = 1e9

// CPAAPIKeyManagementProvider 是 api-key 生命周期与限额策略的服务接口。
type CPAAPIKeyManagementProvider interface {
	// CreateCPAAPIKey 新建 key 并返回完整值；完整值只在此处返回一次。
	CreateCPAAPIKey(ctx context.Context, keyAlias string, customKey string) (entities.CPAAPIKey, string, error)
	// RegenerateCPAAPIKey 用新值替换旧值，alias 与策略原地保留。
	RegenerateCPAAPIKey(ctx context.Context, id int64) (entities.CPAAPIKey, string, error)
	// DeleteCPAAPIKey 从 CPA 删除 key 并标记本地行已删除。
	DeleteCPAAPIKey(ctx context.Context, id int64) error
	// DisableCPAAPIKey 手动禁用，禁止 runner 自动恢复。
	DisableCPAAPIKey(ctx context.Context, id int64) error
	// RestoreCPAAPIKey 手动恢复，清除手动禁用标记。
	RestoreCPAAPIKey(ctx context.Context, id int64) error
	// GetCPAAPIKeyPolicy 返回策略与当前窗口用量。
	GetCPAAPIKeyPolicy(ctx context.Context, id int64) (CPAAPIKeyPolicyView, error)
	// SaveCPAAPIKeyPolicy 校验并保存限额；限额开关固定常开，不再接受调用方传入。
	SaveCPAAPIKeyPolicy(ctx context.Context, id int64, limits keypolicy.Limits) error
	// ListCPAAPIKeyEnforcementLogs 返回审计记录，新事件在前。
	ListCPAAPIKeyEnforcementLogs(ctx context.Context, id int64, limit int) ([]entities.APIKeyEnforcementLog, error)
	// ListCPAAPIKeyPolicySummaries 一次返回所有启用策略 key 的徽标摘要，供列表路由富化。
	ListCPAAPIKeyPolicySummaries(ctx context.Context) (map[int64]CPAAPIKeyPolicySummary, error)
	// ResetCPAAPIKeyQuotas 把所选窗口的限额用量临时清零；ids 为空表示全局（所有 key）。
	ResetCPAAPIKeyQuotas(ctx context.Context, ids []int64, windows []keypolicy.LimitWindow) error
	// UpdateCPAAPIKeyPolicies 批量修改所选 key 的指定窗口限额，未选窗口保持原值。
	UpdateCPAAPIKeyPolicies(ctx context.Context, ids []int64, changes CPAAPIKeyPolicyChanges) error
	// SetCPAAPIKeyQuotaAdjustments 为选中的 key × 窗口设置本周期临时调整额；只对已有正常限额的窗口生效。
	SetCPAAPIKeyQuotaAdjustments(ctx context.Context, ids []int64, windows []keypolicy.LimitWindow, deltaUSD float64) (QuotaAdjustmentResult, error)
	// ClearCPAAPIKeyQuotaAdjustments 提前清除所选 key × 窗口当前生效的临时调整。
	ClearCPAAPIKeyQuotaAdjustments(ctx context.Context, ids []int64, windows []keypolicy.LimitWindow) error
}

// QuotaAdjustmentTarget 标识一个 key × 窗口的调整目标。
type QuotaAdjustmentTarget struct {
	APIKeyID int64
	Window   keypolicy.LimitWindow
}

// QuotaAdjustmentResult 是批量设置临时调整的结果：Skipped 的原因固定是 "no_limit"
//（该 key 在这个窗口本来就没有正常限额，调整无意义）。
type QuotaAdjustmentResult struct {
	Applied []QuotaAdjustmentTarget
	Skipped []QuotaAdjustmentTarget
}

// CPAAPIKeyPolicyChanges 是批量限额修改：Limits 只列出要改的窗口，值为 nil 表示移除该窗口限额。
// 限额开关已固定常开，不再有 Enabled 字段可改。
type CPAAPIKeyPolicyChanges struct {
	Limits map[keypolicy.LimitWindow]*float64
}

// CPAAPIKeyPolicyView 是策略接口的聚合返回值。
type CPAAPIKeyPolicyView struct {
	Policy entities.CPAAPIKeyPolicy
	Limits keypolicy.Limits
	// Effective 是叠加临时调整后的生效限额，供只读查看方（viewer /key-overview/quota）
	// 展示与实际执行一致的 limit/ratio；admin 的 GET/PUT 编辑仍然用 Limits（正常限额）。
	Effective keypolicy.EffectiveLimits
	Usage     keypolicy.UsageByWindow
	Tightest  *keypolicy.TightestLimit
}

// CPAAPIKeyPolicySummary 是列表徽标需要的最小策略摘要。
// 限额开关已常开且响应固定写 true（见 toCPAAPIKeyPolicySummaryResponse），这里不再保留
// Enabled 字段：它从未被任何调用方读取，只会悄悄和响应脱节。
type CPAAPIKeyPolicySummary struct {
	EnforcementState string
	Tightest         *keypolicy.TightestLimit
	// Limits 是逐窗口（日 → 周 → 月）的已配置限额用量，供展开视图逐行展示。
	Limits []keypolicy.TightestLimit
}

type cpaAPIKeyManagementService struct {
	db     *gorm.DB
	client *cpa.Client
	store  *keypolicy.Store
	// keyMutations 串行化所有 key 写操作，避免 Keeper 自己跟自己竞态全量 PUT；
	// 与 keypolicy runner 注入同一实例，双方的 GET→PUT 序列才能互斥。
	keyMutations sync.Locker
	// logger 输出审计写入失败等不阻断主流程的告警。
	logger logrus.FieldLogger
	// now 可注入，测试用。
	now func() time.Time
	// wakeRunner 在限额或清零点变化后立即唤醒限额 runner；nil 时等兜底 tick。
	wakeRunner func()
}

// NewCPAAPIKeyManagementService 组装生命周期服务；catalog 允许为 nil（费用恒 0）。
// keyMutations 为 nil 时使用独享锁；生产 App 必须与 runner 共享同一实例。
func NewCPAAPIKeyManagementService(db *gorm.DB, client *cpa.Client, catalog *pricing.Catalog, keyMutations sync.Locker) CPAAPIKeyManagementProvider {
	return NewCPAAPIKeyManagementServiceWithWaker(db, client, catalog, keyMutations, nil)
}

// NewCPAAPIKeyManagementServiceWithWaker 额外注入限额 runner 的唤醒函数，生产 App 使用。
func NewCPAAPIKeyManagementServiceWithWaker(db *gorm.DB, client *cpa.Client, catalog *pricing.Catalog, keyMutations sync.Locker, wakeRunner func()) CPAAPIKeyManagementProvider {
	if keyMutations == nil {
		keyMutations = &sync.Mutex{}
	}
	return &cpaAPIKeyManagementService{
		db:           db,
		client:       client,
		store:        keypolicy.NewStore(db, catalog),
		keyMutations: keyMutations,
		logger:       logrus.StandardLogger(),
		now:          time.Now,
		wakeRunner:   wakeRunner,
	}
}

// generateAPIKey 生成 sk- 前缀的 43 字符 base64url 随机 key。
func generateAPIKey() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("generate api key: %w", err)
	}
	return "sk-" + base64.RawURLEncoding.EncodeToString(buf), nil
}

// validateCustomKey 限制自定义 key 的长度与字符集。
func validateCustomKey(value string) error {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return errors.New("custom key is required")
	}
	if len(trimmed) > 128 {
		return errors.New("custom key is too long")
	}
	if strings.ContainsAny(trimmed, " \t\r\n") {
		return errors.New("custom key cannot contain whitespace")
	}
	return nil
}

// fetchCPAKeyList 读取 CPA 当前 key 列表，失败统一包装为 ErrCPARequestFailed。
func (s *cpaAPIKeyManagementService) fetchCPAKeyList(ctx context.Context) ([]string, error) {
	result, err := s.client.FetchManagementAPIKeys(ctx)
	if err != nil {
		return nil, fmt.Errorf("%w: fetch api keys: %v", ErrCPARequestFailed, err)
	}
	return result.Payload.APIKeys, nil
}

// replaceCPAKeyList 全量替换并返回错误包装；只接受刚 GET 并追加过的非空列表。
func (s *cpaAPIKeyManagementService) replaceCPAKeyList(ctx context.Context, keys []string) error {
	// PUT {"items":[]} 会清空 CPA 全部 key，宁可失败也绝不放行空列表。
	if len(keys) == 0 {
		return errors.New("refuse to replace cpa api keys with empty list")
	}
	if _, err := s.client.ReplaceManagementAPIKeys(ctx, keys); err != nil {
		return fmt.Errorf("%w: replace api keys: %v", ErrCPARequestFailed, err)
	}
	return nil
}

// syncLocalKeys 用与 metadata sync 相同的 upsert 路径落地本地行。
func (s *cpaAPIKeyManagementService) syncLocalKeys(keys []string) error {
	if err := repository.SyncCPAAPIKeys(s.db, keys, s.now()); err != nil {
		return fmt.Errorf("sync local api keys: %w", err)
	}
	return nil
}

// ensurePolicyRow 给还没有策略行的 key 补一条空策略。
func (s *cpaAPIKeyManagementService) ensurePolicyRow(cpaAPIKeyID int64) (entities.CPAAPIKeyPolicy, error) {
	policy, err := repository.FindCPAAPIKeyPolicy(s.db, cpaAPIKeyID)
	if err == nil {
		return policy, nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return entities.CPAAPIKeyPolicy{}, err
	}
	created := entities.CPAAPIKeyPolicy{
		// Enabled 固定 true：限额开关已不可关闭，新建的空策略行同样常开。
		CPAAPIKeyID: cpaAPIKeyID, Limits: "[]", Enabled: true,
		EnforcementState: string(keypolicy.StateActive),
	}
	if err := repository.UpsertCPAAPIKeyPolicy(s.db, &created); err != nil {
		return entities.CPAAPIKeyPolicy{}, err
	}
	// Upsert 不会回填更新时间等字段，需要最新状态时一律回读。
	return repository.FindCPAAPIKeyPolicy(s.db, cpaAPIKeyID)
}

// writeEnforcementLog 落一条审计记录，失败只记日志不阻断主流程。
func (s *cpaAPIKeyManagementService) writeEnforcementLog(cpaAPIKeyID int64, action, reason, detail string) {
	err := repository.InsertAPIKeyEnforcementLog(s.db, entities.APIKeyEnforcementLog{
		CPAAPIKeyID: cpaAPIKeyID, Action: action, Reason: reason, Detail: detail, CreatedAt: s.now(),
	})
	if err != nil {
		// 审计失败不影响业务结果，但必须留下日志。
		s.logger.WithError(err).WithField("cpa_api_key_id", cpaAPIKeyID).Warn("write api key enforcement log failed")
	}
}

func (s *cpaAPIKeyManagementService) CreateCPAAPIKey(ctx context.Context, keyAlias string, customKey string) (entities.CPAAPIKey, string, error) {
	s.keyMutations.Lock()
	defer s.keyMutations.Unlock()
	newKey := strings.TrimSpace(customKey)
	if newKey == "" {
		generated, err := generateAPIKey()
		if err != nil {
			return entities.CPAAPIKey{}, "", err
		}
		newKey = generated
	} else if err := validateCustomKey(newKey); err != nil {
		return entities.CPAAPIKey{}, "", fmt.Errorf("%w: %v", ErrInvalidInput, err)
	}
	current, err := s.fetchCPAKeyList(ctx)
	if err != nil {
		return entities.CPAAPIKey{}, "", err
	}
	if slices.Contains(current, newKey) {
		return entities.CPAAPIKey{}, "", fmt.Errorf("%w: api key already exists", ErrInvalidInput)
	}
	// 全量 PUT 只发刚 GET 到的列表加新 key，绝不凭空构造列表。
	next := append(current, newKey)
	if err := s.replaceCPAKeyList(ctx, next); err != nil {
		return entities.CPAAPIKey{}, "", err
	}
	if err := s.syncLocalKeys(next); err != nil {
		return entities.CPAAPIKey{}, "", err
	}
	row, err := repository.FindActiveCPAAPIKeyByValue(s.db, newKey)
	if err != nil {
		return entities.CPAAPIKey{}, "", err
	}
	if strings.TrimSpace(keyAlias) != "" && strings.TrimSpace(keyAlias) != row.KeyAlias {
		if err := repository.UpdateCPAAPIKeyAlias(s.db, row.ID, strings.TrimSpace(keyAlias)); err != nil {
			return entities.CPAAPIKey{}, "", err
		}
		row.KeyAlias = strings.TrimSpace(keyAlias)
	}
	return row, newKey, nil
}

func (s *cpaAPIKeyManagementService) RegenerateCPAAPIKey(ctx context.Context, id int64) (entities.CPAAPIKey, string, error) {
	s.keyMutations.Lock()
	defer s.keyMutations.Unlock()
	row, err := repository.FindActiveCPAAPIKeyByID(s.db, id)
	if err != nil {
		return entities.CPAAPIKey{}, "", err
	}
	policy, err := s.ensurePolicyRow(id)
	if err != nil {
		return entities.CPAAPIKey{}, "", err
	}
	// 手动或超限禁用的 key 不在 CPA 里，PATCH 没有匹配目标，直接拒绝。
	if policy.EnforcementState != string(keypolicy.StateActive) {
		return entities.CPAAPIKey{}, "", ErrKeyDisabled
	}
	newKey, err := generateAPIKey()
	if err != nil {
		return entities.CPAAPIKey{}, "", err
	}
	if _, err := s.client.UpdateManagementAPIKey(ctx, row.APIKey, newKey); err != nil {
		return entities.CPAAPIKey{}, "", fmt.Errorf("%w: update api key: %v", ErrCPARequestFailed, err)
	}
	if err := repository.UpdateCPAAPIKeyValue(s.db, id, newKey); err != nil {
		return entities.CPAAPIKey{}, "", err
	}
	updated, err := repository.FindActiveCPAAPIKeyByID(s.db, id)
	if err != nil {
		return entities.CPAAPIKey{}, "", err
	}
	return updated, newKey, nil
}

func (s *cpaAPIKeyManagementService) DeleteCPAAPIKey(ctx context.Context, id int64) error {
	s.keyMutations.Lock()
	defer s.keyMutations.Unlock()
	row, err := repository.FindActiveCPAAPIKeyByID(s.db, id)
	if err != nil {
		return err
	}
	// 先 GET 再决定是否 DELETE：禁用过的 key 本就不在 CPA 里，直接 DELETE 会 404。
	current, err := s.fetchCPAKeyList(ctx)
	if err != nil {
		return err
	}
	present := slices.Contains(current, row.APIKey)
	if present {
		if _, err := s.client.DeleteManagementAPIKey(ctx, row.APIKey); err != nil {
			return fmt.Errorf("%w: delete api key: %v", ErrCPARequestFailed, err)
		}
	}
	// 策略行必须先于 sync 删除，否则 sync 会把该 key 视为“被策略有意移出”而留下 active 幽灵行。
	if err := repository.DeleteCPAAPIKeyPolicy(s.db, id); err != nil {
		return err
	}
	if present {
		// 删除成功后回读权威列表再落地本地行。
		if current, err = s.fetchCPAKeyList(ctx); err != nil {
			return err
		}
	}
	if err := s.syncLocalKeys(current); err != nil {
		return err
	}
	// 审计日志保留。
	return nil
}

func (s *cpaAPIKeyManagementService) DisableCPAAPIKey(ctx context.Context, id int64) error {
	s.keyMutations.Lock()
	defer s.keyMutations.Unlock()
	row, err := repository.FindActiveCPAAPIKeyByID(s.db, id)
	if err != nil {
		return err
	}
	// TOCTOU：周期 metadata sync 不持有本服务的互斥锁。必须先把 disabled_manual 落库
	//（sync 的策略保护立即生效），再向 CPA 发 DELETE；若先删后写，两次写之间的 sync tick
	// 会因为状态仍是 active 把本地行软删掉，管理员看到的是 key 凭空消失而不是“手动禁用”。
	if _, err := s.ensurePolicyRow(id); err != nil {
		return err
	}
	if err := repository.UpdateCPAAPIKeyPolicyLifecycle(s.db, id, string(keypolicy.StateDisabledManual), "", true, s.now()); err != nil {
		return err
	}
	// 先 GET 再决定是否 DELETE：重复禁用或外部删除时 key 已不在 CPA，跳过 DELETE 直接成功。
	// DELETE 失败时状态已是 disabled_manual：本地行被策略保护、key 仍可见，重试禁用即可收敛。
	current, err := s.fetchCPAKeyList(ctx)
	if err != nil {
		return err
	}
	if slices.Contains(current, row.APIKey) {
		if _, err := s.client.DeleteManagementAPIKey(ctx, row.APIKey); err != nil {
			return fmt.Errorf("%w: delete api key: %v", ErrCPARequestFailed, err)
		}
	}
	s.writeEnforcementLog(id, "disabled", "admin_action", "")
	return nil
}

func (s *cpaAPIKeyManagementService) RestoreCPAAPIKey(ctx context.Context, id int64) error {
	s.keyMutations.Lock()
	defer s.keyMutations.Unlock()
	row, err := repository.FindActiveCPAAPIKeyByID(s.db, id)
	if err != nil {
		return err
	}
	current, err := s.fetchCPAKeyList(ctx)
	if err != nil {
		return err
	}
	missing := !slices.Contains(current, row.APIKey)
	// 只有 key 确实不在 CPA 时才做 GET+追加 的全量 PUT。
	if missing {
		if err := s.replaceCPAKeyList(ctx, append(current, row.APIKey)); err != nil {
			return err
		}
	}
	if _, err := s.ensurePolicyRow(id); err != nil {
		return err
	}
	// 定向写生命周期状态：整行 upsert 会把读取时的运行时字段原样写回，同样存在覆盖并发写的问题。
	if err := repository.UpdateCPAAPIKeyPolicyLifecycle(s.db, id, string(keypolicy.StateActive), "", false, s.now()); err != nil {
		return err
	}
	s.writeEnforcementLog(id, "restored", "admin_action", "")
	return nil
}

func (s *cpaAPIKeyManagementService) GetCPAAPIKeyPolicy(ctx context.Context, id int64) (CPAAPIKeyPolicyView, error) {
	if _, err := repository.FindActiveCPAAPIKeyByID(s.db, id); err != nil {
		return CPAAPIKeyPolicyView{}, err
	}
	policy, err := s.ensurePolicyRow(id)
	if err != nil {
		return CPAAPIKeyPolicyView{}, err
	}
	limits, err := keypolicy.ParseLimits(policy.Limits)
	if err != nil {
		return CPAAPIKeyPolicyView{}, err
	}
	now := s.now()
	daily, weekly, monthly := keypolicy.DailyWindow(now), keypolicy.WeeklyWindow(now), keypolicy.MonthlyWindow(now)
	usage, err := s.store.SingleKeyUsage(ctx, id, daily, weekly, monthly)
	if err != nil {
		return CPAAPIKeyPolicyView{}, err
	}
	adjustments, err := s.store.ActiveAdjustments(daily, weekly, monthly)
	if err != nil {
		return CPAAPIKeyPolicyView{}, err
	}
	// Tightest/Effective 展示生效值（叠加临时调整）；Limits 仍是正常存储的限额，是这个视图可编辑的配置。
	effective := keypolicy.ApplyAdjustments(limits, adjustments[id])
	return CPAAPIKeyPolicyView{
		Policy: policy, Limits: limits, Effective: effective, Usage: usage, Tightest: effective.Tightest(usage),
	}, nil
}

func (s *cpaAPIKeyManagementService) SaveCPAAPIKeyPolicy(ctx context.Context, id int64, limits keypolicy.Limits) error {
	s.keyMutations.Lock()
	defer s.keyMutations.Unlock()
	if _, err := repository.FindActiveCPAAPIKeyByID(s.db, id); err != nil {
		return err
	}
	if err := limits.Validate(); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidInput, err)
	}
	encoded := "[]"
	if len(limits) > 0 {
		raw, err := json.Marshal(limits)
		if err != nil {
			return fmt.Errorf("encode api key limits: %w", err)
		}
		encoded = string(raw)
	}
	policy, err := s.ensurePolicyRow(id)
	if err != nil {
		return err
	}
	// 保存前的正常限额：和新值比较找出被移除的窗口，顺带清掉这些窗口残留的临时调整
	// （见 removedLimitWindows 的注释，item 3）。
	before, err := keypolicy.ParseLimits(policy.Limits)
	if err != nil {
		return err
	}
	removed := removedLimitWindows(before, limits)
	// 定向更新配置列：整行 upsert 会把读取时的 enforcement_state/admin_disabled/
	// last_evaluated_at 原样写回，覆盖 runner 在读与写之间落下的并发禁用（保存期间
	// runner 不持有 keyMutations）。禁用态是否解除由 runner 的恢复流程独立裁决。
	// enabled 不再接受调用方输入，UpdateCPAAPIKeyPolicyConfig 内部固定写 true。
	// 限额更新和调整清理放在同一个事务里：要么都生效，要么都不生效。
	return s.db.Transaction(func(tx *gorm.DB) error {
		if err := repository.UpdateCPAAPIKeyPolicyConfig(tx, id, encoded); err != nil {
			return err
		}
		if len(removed) == 0 {
			return nil
		}
		return repository.DeleteAPIKeyQuotaAdjustments(tx, []int64{id}, limitWindowStrings(removed))
	})
}

func (s *cpaAPIKeyManagementService) ListCPAAPIKeyEnforcementLogs(_ context.Context, id int64, limit int) ([]entities.APIKeyEnforcementLog, error) {
	return repository.ListAPIKeyEnforcementLogs(s.db, id, limit)
}

// ListCPAAPIKeyPolicySummaries 一次策略查询加一次全量窗口用量查询，拼出每个 key 的徽标摘要。
func (s *cpaAPIKeyManagementService) ListCPAAPIKeyPolicySummaries(ctx context.Context) (map[int64]CPAAPIKeyPolicySummary, error) {
	rows, err := repository.ListEnabledCPAAPIKeyPolicies(s.db)
	if err != nil {
		return nil, err
	}
	now := s.now()
	daily, weekly, monthly := keypolicy.DailyWindow(now), keypolicy.WeeklyWindow(now), keypolicy.MonthlyWindow(now)
	usage, err := s.store.PerKeyUsage(ctx, daily, weekly, monthly)
	if err != nil {
		return nil, err
	}
	// 临时调整额一次性加载，所有 key 的徽标共用同一份当前周期快照。
	adjustments, err := s.store.ActiveAdjustments(daily, weekly, monthly)
	if err != nil {
		return nil, err
	}
	summaries := make(map[int64]CPAAPIKeyPolicySummary, len(rows))
	for _, row := range rows {
		limits, err := keypolicy.ParseLimits(row.Limits)
		if err != nil {
			// 单个 key 的限额 JSON 损坏只丢掉最紧张限额徽标，状态照常返回。
			summaries[row.CPAAPIKeyID] = CPAAPIKeyPolicySummary{EnforcementState: row.EnforcementState}
			continue
		}
		// 徽标展示生效值（叠加临时调整）：value 是生效限额，Base/Adjustment 随 TightestLimit 一并带出。
		effective := keypolicy.ApplyAdjustments(limits, adjustments[row.CPAAPIKeyID])
		summaries[row.CPAAPIKeyID] = CPAAPIKeyPolicySummary{
			EnforcementState: row.EnforcementState,
			Tightest:         effective.Tightest(usage[row.CPAAPIKeyID]),
			Limits:           effective.WindowUsages(usage[row.CPAAPIKeyID]),
		}
	}
	return summaries, nil
}

// limitWindowOrder 固定窗口校验与排序顺序：日 → 周 → 月。
var limitWindowOrder = []keypolicy.LimitWindow{keypolicy.LimitWindowDaily, keypolicy.LimitWindowWeekly, keypolicy.LimitWindowMonthly}

func currentLimitWindow(kind keypolicy.LimitWindow, now time.Time) keypolicy.Window {
	switch kind {
	case keypolicy.LimitWindowWeekly:
		return keypolicy.WeeklyWindow(now)
	case keypolicy.LimitWindowMonthly:
		return keypolicy.MonthlyWindow(now)
	}
	return keypolicy.DailyWindow(now)
}

// normalizeLimitWindows 校验窗口名并去重排序；空集合或未知窗口返回 ErrInvalidInput。
func normalizeLimitWindows(windows []keypolicy.LimitWindow) ([]keypolicy.LimitWindow, error) {
	selected := make(map[keypolicy.LimitWindow]bool, len(windows))
	for _, window := range windows {
		if !slices.Contains(limitWindowOrder, window) {
			return nil, fmt.Errorf("%w: unknown window %q", ErrInvalidInput, window)
		}
		selected[window] = true
	}
	if len(selected) == 0 {
		return nil, fmt.Errorf("%w: at least one window is required", ErrInvalidInput)
	}
	result := make([]keypolicy.LimitWindow, 0, len(selected))
	for _, window := range limitWindowOrder {
		if selected[window] {
			result = append(result, window)
		}
	}
	return result, nil
}

// uniqueIDs 去重并保持首次出现顺序。
func uniqueIDs(ids []int64) []int64 {
	seen := make(map[int64]bool, len(ids))
	result := make([]int64, 0, len(ids))
	for _, id := range ids {
		if !seen[id] {
			seen[id] = true
			result = append(result, id)
		}
	}
	return result
}

// limitWindowStrings 把窗口枚举转成仓储层按字符串匹配需要的切片。
func limitWindowStrings(windows []keypolicy.LimitWindow) []string {
	names := make([]string, 0, len(windows))
	for _, window := range windows {
		names = append(names, string(window))
	}
	return names
}

func joinLimitWindows(windows []keypolicy.LimitWindow) string {
	return strings.Join(limitWindowStrings(windows), ",")
}

// removedLimitWindows 比较调整前后的限额集合，返回在 before 里配置了限额、但 after 里已经
// 不存在的窗口。这些窗口残留的临时调整需要跟着一起清掉：否则同一周期内先删掉某窗口的限额、
// 再把限额加回来，旧的 adjustment 行会原样复活（window_key 没变，还在当前周期内生效）。
func removedLimitWindows(before, after keypolicy.Limits) []keypolicy.LimitWindow {
	afterHas := make(map[keypolicy.LimitWindow]bool, len(after))
	for _, limit := range after {
		afterHas[limit.Window] = true
	}
	removed := make([]keypolicy.LimitWindow, 0, len(before))
	for _, limit := range before {
		if !afterHas[limit.Window] {
			removed = append(removed, limit.Window)
		}
	}
	return removed
}

func (s *cpaAPIKeyManagementService) wake() {
	if s.wakeRunner != nil {
		s.wakeRunner()
	}
}

// ResetCPAAPIKeyQuotas 只写清零点，不删除任何 usage 数据：限额统计从清零时刻重新起算，
// 排行与用量统计照常读取完整 usage_events。周期翻转后清零点自然失效。
func (s *cpaAPIKeyManagementService) ResetCPAAPIKeyQuotas(_ context.Context, ids []int64, windows []keypolicy.LimitWindow) error {
	windows, err := normalizeLimitWindows(windows)
	if err != nil {
		return err
	}
	ids = uniqueIDs(ids)
	global := len(ids) == 0
	targets := ids
	if global {
		keys, err := repository.ListActiveCPAAPIKeys(s.db)
		if err != nil {
			return err
		}
		targets = make([]int64, 0, len(keys))
		for _, key := range keys {
			targets = append(targets, key.ID)
		}
	} else {
		for _, id := range ids {
			if id <= 0 {
				return ErrInvalidID
			}
			if _, err := repository.FindActiveCPAAPIKeyByID(s.db, id); err != nil {
				return err
			}
		}
	}

	now := s.now()
	scopeIDs := ids
	if global {
		scopeIDs = []int64{entities.APIKeyQuotaResetGlobalID}
	}
	rows := make([]entities.APIKeyQuotaReset, 0, len(scopeIDs)*len(windows))
	currentKeys := make(map[string]string, len(limitWindowOrder))
	for _, window := range limitWindowOrder {
		currentKeys[string(window)] = keypolicy.WindowKey(currentLimitWindow(window, now))
	}
	for _, id := range scopeIDs {
		for _, window := range windows {
			rows = append(rows, entities.APIKeyQuotaReset{
				CPAAPIKeyID: id, Window: string(window), WindowKey: currentKeys[string(window)],
				ResetAt: now, CreatedAt: now, UpdatedAt: now,
			})
		}
	}
	if err := repository.UpsertAPIKeyQuotaResets(s.db, rows); err != nil {
		return fmt.Errorf("save api key quota resets: %w", err)
	}
	// 过期周期的清零点已不参与统计，顺手清理避免表无限增长；失败不影响本次重置。
	if err := repository.PruneStaleAPIKeyQuotaResets(s.db, currentKeys); err != nil {
		s.logger.WithError(err).Warn("prune stale api key quota resets failed")
	}
	detail := joinLimitWindows(windows)
	if global {
		detail += " (all keys)"
	}
	for _, id := range targets {
		s.writeEnforcementLog(id, "quota_reset", "admin_action", detail)
	}
	// 清零后超限禁用的 key 不再超限，立即唤醒 runner 让它马上恢复。
	s.wake()
	return nil
}

// UpdateCPAAPIKeyPolicies 在 keyMutations 下逐 key 合并限额：只改动请求里列出的窗口，
// 先整体校验再在单个事务里写入，任一 key 非法则全部不生效。
func (s *cpaAPIKeyManagementService) UpdateCPAAPIKeyPolicies(_ context.Context, ids []int64, changes CPAAPIKeyPolicyChanges) error {
	ids = uniqueIDs(ids)
	if len(ids) == 0 {
		return fmt.Errorf("%w: at least one api key is required", ErrInvalidInput)
	}
	// enabled 开关已移除：没有 limits 变更就真的无事可做。
	if len(changes.Limits) == 0 {
		return fmt.Errorf("%w: nothing to update", ErrInvalidInput)
	}
	changedWindows := make([]keypolicy.LimitWindow, 0, len(changes.Limits))
	for window, value := range changes.Limits {
		changedWindows = append(changedWindows, window)
		if value != nil && !(*value > 0) {
			return fmt.Errorf("%w: limit value must be positive for %s", ErrInvalidInput, window)
		}
	}
	if len(changedWindows) > 0 {
		if _, err := normalizeLimitWindows(changedWindows); err != nil {
			return err
		}
	}

	s.keyMutations.Lock()
	defer s.keyMutations.Unlock()
	type pendingUpdate struct {
		id      int64
		limits  string
		removed []keypolicy.LimitWindow
	}
	updates := make([]pendingUpdate, 0, len(ids))
	for _, id := range ids {
		if id <= 0 {
			return ErrInvalidID
		}
		if _, err := repository.FindActiveCPAAPIKeyByID(s.db, id); err != nil {
			return err
		}
		policy, err := s.ensurePolicyRow(id)
		if err != nil {
			return err
		}
		// policy 只还需要读 Limits 做合并；enabled 已固定常开，不再从这行读出或写回。
		current, err := keypolicy.ParseLimits(policy.Limits)
		if err != nil {
			return err
		}
		merged := make(keypolicy.Limits, 0, len(limitWindowOrder))
		for _, window := range limitWindowOrder {
			if value, changed := changes.Limits[window]; changed {
				if value != nil {
					merged = append(merged, keypolicy.Limit{Type: keypolicy.LimitTypeCost, Window: window, Value: *value})
				}
				continue
			}
			for _, limit := range current {
				if limit.Window == window {
					merged = append(merged, limit)
				}
			}
		}
		if err := merged.Validate(); err != nil {
			return fmt.Errorf("%w: %v", ErrInvalidInput, err)
		}
		encoded := "[]"
		if len(merged) > 0 {
			raw, err := json.Marshal(merged)
			if err != nil {
				return fmt.Errorf("encode api key limits: %w", err)
			}
			encoded = string(raw)
		}
		// 本次合并移除的窗口要跟着清掉残留的临时调整，否则同周期内删了限额再加回来，
		// 旧 adjustment 会原样复活（item 3）。
		removed := removedLimitWindows(current, merged)
		updates = append(updates, pendingUpdate{id: id, limits: encoded, removed: removed})
	}
	if err := s.db.Transaction(func(tx *gorm.DB) error {
		for _, update := range updates {
			if err := repository.UpdateCPAAPIKeyPolicyConfig(tx, update.id, update.limits); err != nil {
				return err
			}
			if len(update.removed) == 0 {
				continue
			}
			if err := repository.DeleteAPIKeyQuotaAdjustments(tx, []int64{update.id}, limitWindowStrings(update.removed)); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		return err
	}
	s.wake()
	return nil
}

// SetCPAAPIKeyQuotaAdjustments 给选中 key × 窗口设置本周期的临时调整额：只对该 key
// 已配置正常限额的窗口生效，没有限额的窗口会被跳过（调整不能无中生有造出一条限额）。
// 再次为同一 key × 窗口设置会替换上一次的 delta，不会累加。
func (s *cpaAPIKeyManagementService) SetCPAAPIKeyQuotaAdjustments(_ context.Context, ids []int64, windows []keypolicy.LimitWindow, deltaUSD float64) (QuotaAdjustmentResult, error) {
	ids = uniqueIDs(ids)
	if len(ids) == 0 {
		return QuotaAdjustmentResult{}, fmt.Errorf("%w: at least one api key is required", ErrInvalidInput)
	}
	windows, err := normalizeLimitWindows(windows)
	if err != nil {
		return QuotaAdjustmentResult{}, err
	}
	// NaN/±Inf/0 在这里挡住：ApplyAdjustments 本身不做这层校验，交给调用入口统一拒绝。
	if math.IsNaN(deltaUSD) || math.IsInf(deltaUSD, 0) || deltaUSD == 0 {
		return QuotaAdjustmentResult{}, fmt.Errorf("%w: deltaUsd must be a finite, non-zero number", ErrInvalidInput)
	}
	// 再封一个量级上限：limit + delta 本身不会溢出到 Inf，但没有上限就挡不住离谱输入，
	// 且前端用同样的 1e9 上限做校验，这里必须保持一致。
	if math.Abs(deltaUSD) > quotaAdjustmentMaxAbsDeltaUSD {
		return QuotaAdjustmentResult{}, fmt.Errorf("%w: deltaUsd must not exceed %.0f in magnitude", ErrInvalidInput, quotaAdjustmentMaxAbsDeltaUSD)
	}

	now := s.now()
	currentKeys := make(map[string]string, len(limitWindowOrder))
	for _, window := range limitWindowOrder {
		currentKeys[string(window)] = keypolicy.WindowKey(currentLimitWindow(window, now))
	}

	var result QuotaAdjustmentResult
	rows := make([]entities.APIKeyQuotaAdjustment, 0, len(ids)*len(windows))
	appliedByKey := make(map[int64][]keypolicy.LimitWindow, len(ids))
	for _, id := range ids {
		if id <= 0 {
			return QuotaAdjustmentResult{}, ErrInvalidID
		}
		if _, err := repository.FindActiveCPAAPIKeyByID(s.db, id); err != nil {
			return QuotaAdjustmentResult{}, err
		}
		policy, err := s.ensurePolicyRow(id)
		if err != nil {
			return QuotaAdjustmentResult{}, err
		}
		limits, err := keypolicy.ParseLimits(policy.Limits)
		if err != nil {
			return QuotaAdjustmentResult{}, err
		}
		configured := make(map[keypolicy.LimitWindow]bool, len(limits))
		for _, limit := range limits {
			configured[limit.Window] = true
		}
		for _, window := range windows {
			target := QuotaAdjustmentTarget{APIKeyID: id, Window: window}
			if !configured[window] {
				result.Skipped = append(result.Skipped, target)
				continue
			}
			result.Applied = append(result.Applied, target)
			appliedByKey[id] = append(appliedByKey[id], window)
			rows = append(rows, entities.APIKeyQuotaAdjustment{
				CPAAPIKeyID: id, Window: string(window), WindowKey: currentKeys[string(window)],
				DeltaUSD: deltaUSD, CreatedAt: now, UpdatedAt: now,
			})
		}
	}

	if err := s.db.Transaction(func(tx *gorm.DB) error {
		return repository.UpsertAPIKeyQuotaAdjustments(tx, rows)
	}); err != nil {
		return QuotaAdjustmentResult{}, fmt.Errorf("save api key quota adjustments: %w", err)
	}
	for _, id := range ids {
		if applied := appliedByKey[id]; len(applied) > 0 {
			detail := fmt.Sprintf("%s %+.2f", joinLimitWindows(applied), deltaUSD)
			s.writeEnforcementLog(id, "quota_adjusted", "admin_action", detail)
		}
	}
	// 调整可能让超限的 key 立即转回未超限（或反过来超限），立即唤醒 runner 重新评估。
	s.wake()
	return result, nil
}

// ClearCPAAPIKeyQuotaAdjustments 提前清除所选 key × 窗口当前生效的临时调整，
// 清除后的下一轮评估会按正常限额重新判定。
func (s *cpaAPIKeyManagementService) ClearCPAAPIKeyQuotaAdjustments(_ context.Context, ids []int64, windows []keypolicy.LimitWindow) error {
	ids = uniqueIDs(ids)
	if len(ids) == 0 {
		return fmt.Errorf("%w: at least one api key is required", ErrInvalidInput)
	}
	windows, err := normalizeLimitWindows(windows)
	if err != nil {
		return err
	}
	for _, id := range ids {
		if id <= 0 {
			return ErrInvalidID
		}
		if _, err := repository.FindActiveCPAAPIKeyByID(s.db, id); err != nil {
			return err
		}
	}
	windowNames := make([]string, 0, len(windows))
	for _, window := range windows {
		windowNames = append(windowNames, string(window))
	}
	if err := repository.DeleteAPIKeyQuotaAdjustments(s.db, ids, windowNames); err != nil {
		return fmt.Errorf("clear api key quota adjustments: %w", err)
	}
	detail := joinLimitWindows(windows)
	for _, id := range ids {
		s.writeEnforcementLog(id, "quota_adjustment_cleared", "admin_action", detail)
	}
	s.wake()
	return nil
}
