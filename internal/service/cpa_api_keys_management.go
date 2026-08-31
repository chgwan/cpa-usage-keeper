package service

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
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
	// SaveCPAAPIKeyPolicy 校验并保存限额与开关。
	SaveCPAAPIKeyPolicy(ctx context.Context, id int64, limits keypolicy.Limits, enabled bool) error
	// ListCPAAPIKeyEnforcementLogs 返回审计记录，新事件在前。
	ListCPAAPIKeyEnforcementLogs(ctx context.Context, id int64, limit int) ([]entities.APIKeyEnforcementLog, error)
	// ListCPAAPIKeyPolicySummaries 一次返回所有启用策略 key 的徽标摘要，供列表路由富化。
	ListCPAAPIKeyPolicySummaries(ctx context.Context) (map[int64]CPAAPIKeyPolicySummary, error)
}

// CPAAPIKeyPolicyView 是策略接口的聚合返回值。
type CPAAPIKeyPolicyView struct {
	Policy   entities.CPAAPIKeyPolicy
	Limits   keypolicy.Limits
	Usage    keypolicy.UsageByWindow
	Tightest *keypolicy.TightestLimit
}

// CPAAPIKeyPolicySummary 是列表徽标需要的最小策略摘要。
type CPAAPIKeyPolicySummary struct {
	Enabled          bool
	EnforcementState string
	Tightest         *keypolicy.TightestLimit
}

type cpaAPIKeyManagementService struct {
	db     *gorm.DB
	client *cpa.Client
	store  *keypolicy.Store
	// mu 串行化所有 key 写操作，避免 Keeper 自己跟自己竞态全量 PUT。
	mu sync.Mutex
	// logger 输出审计写入失败等不阻断主流程的告警。
	logger logrus.FieldLogger
	// now 可注入，测试用。
	now func() time.Time
}

// NewCPAAPIKeyManagementService 组装生命周期服务；catalog 允许为 nil（费用恒 0）。
func NewCPAAPIKeyManagementService(db *gorm.DB, client *cpa.Client, catalog *pricing.Catalog) CPAAPIKeyManagementProvider {
	return &cpaAPIKeyManagementService{
		db:     db,
		client: client,
		store:  keypolicy.NewStore(db, catalog),
		logger: logrus.StandardLogger(),
		now:    time.Now,
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
	s.mu.Lock()
	defer s.mu.Unlock()
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
	s.mu.Lock()
	defer s.mu.Unlock()
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
	s.mu.Lock()
	defer s.mu.Unlock()
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
	s.mu.Lock()
	defer s.mu.Unlock()
	row, err := repository.FindActiveCPAAPIKeyByID(s.db, id)
	if err != nil {
		return err
	}
	// 先 GET：重复禁用或外部删除时 key 已不在 CPA，跳过 DELETE 直接落禁用状态，避免无谓的 404/502。
	current, err := s.fetchCPAKeyList(ctx)
	if err != nil {
		return err
	}
	if slices.Contains(current, row.APIKey) {
		if _, err := s.client.DeleteManagementAPIKey(ctx, row.APIKey); err != nil {
			return fmt.Errorf("%w: delete api key: %v", ErrCPARequestFailed, err)
		}
	}
	if _, err := s.ensurePolicyRow(id); err != nil {
		return err
	}
	policy, err := repository.FindCPAAPIKeyPolicy(s.db, id)
	if err != nil {
		return err
	}
	policy.AdminDisabled = true
	policy.EnforcementState = string(keypolicy.StateDisabledManual)
	if err := repository.UpsertCPAAPIKeyPolicy(s.db, &policy); err != nil {
		return err
	}
	s.writeEnforcementLog(id, "disabled", "admin_action", "")
	return nil
}

func (s *cpaAPIKeyManagementService) RestoreCPAAPIKey(ctx context.Context, id int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
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
	policy, err := repository.FindCPAAPIKeyPolicy(s.db, id)
	if err != nil {
		return err
	}
	policy.AdminDisabled = false
	policy.EnforcementState = string(keypolicy.StateActive)
	policy.DisabledWindowKey = ""
	if err := repository.UpsertCPAAPIKeyPolicy(s.db, &policy); err != nil {
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
	usage, err := s.store.SingleKeyUsage(ctx, id, keypolicy.DailyWindow(now), keypolicy.MonthlyWindow(now))
	if err != nil {
		return CPAAPIKeyPolicyView{}, err
	}
	return CPAAPIKeyPolicyView{
		Policy: policy, Limits: limits, Usage: usage, Tightest: limits.Tightest(usage),
	}, nil
}

func (s *cpaAPIKeyManagementService) SaveCPAAPIKeyPolicy(ctx context.Context, id int64, limits keypolicy.Limits, enabled bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
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
	policy.Limits = encoded
	policy.Enabled = enabled
	// 保存策略时若此前被手动禁用则保持；仅清除超限禁用的标记由 runner 恢复流程处理。
	return repository.UpsertCPAAPIKeyPolicy(s.db, &policy)
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
	usage, err := s.store.PerKeyUsage(ctx, keypolicy.DailyWindow(now), keypolicy.MonthlyWindow(now))
	if err != nil {
		return nil, err
	}
	summaries := make(map[int64]CPAAPIKeyPolicySummary, len(rows))
	for _, row := range rows {
		limits, err := keypolicy.ParseLimits(row.Limits)
		if err != nil {
			// 单个 key 的限额 JSON 损坏只丢掉最紧张限额徽标，状态照常返回。
			summaries[row.CPAAPIKeyID] = CPAAPIKeyPolicySummary{Enabled: row.Enabled, EnforcementState: row.EnforcementState}
			continue
		}
		summaries[row.CPAAPIKeyID] = CPAAPIKeyPolicySummary{
			Enabled:          row.Enabled,
			EnforcementState: row.EnforcementState,
			Tightest:         limits.Tightest(usage[row.CPAAPIKeyID]),
		}
	}
	return summaries, nil
}
