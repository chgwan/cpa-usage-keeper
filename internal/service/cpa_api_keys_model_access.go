package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"

	"cpa-usage-keeper/internal/cpa"
	"cpa-usage-keeper/internal/entities"
	"cpa-usage-keeper/internal/repository"

	"github.com/sirupsen/logrus"
	"gorm.io/gorm"
)

const (
	keyModelAccessPolicySchemaVersion = 2
	keyModelAccessMaxPatternLength    = 256
	keyModelAccessPersistPollAttempts = 20
	keyModelAccessPersistPollInterval = 150 * time.Millisecond
)

var (
	// ErrKeyModelAccessUnavailable indicates the key-model-access plugin is not installed or enabled on CPA.
	ErrKeyModelAccessUnavailable = errors.New("key-model-access plugin is unavailable")
	// ErrKeyModelAccessConflict indicates a concurrent policy revision change (HTTP 412 from plugin).
	ErrKeyModelAccessConflict = errors.New("key-model-access policy revision conflict")
	// ErrKeyModelAccessNotPersistent indicates reload was requested while policy_file is not configured.
	ErrKeyModelAccessNotPersistent = errors.New("key-model-access policy_file is not configured")
)

// KeyModelAccessRule holds normalized allow/deny model glob patterns for a single API key.
type KeyModelAccessRule struct {
	AllowModels []string
	DenyModels  []string
}

// KeyModelAccessStatusView summarizes runtime & persistence metadata reported by the plugin.
type KeyModelAccessStatusView struct {
	Version               string
	PolicyCount           int
	Revision              uint64
	PolicyFile            string
	PersistentUpdates     bool
	Source                string
	UpdatedAt             string
	FailClosed            bool
	LastError             string
	PersistenceSetupError string
}

// KeyModelAccessState is the aggregated view returned to Settings -> API Key Settings.
type KeyModelAccessState struct {
	Available   bool
	Status      *KeyModelAccessStatusView
	Models      []string
	ModelsError string
	Policies    map[int64]KeyModelAccessRule
}

// KeyModelAccessLifecycleHook coordinates model access rules when Keeper mutates native API keys.
type KeyModelAccessLifecycleHook interface {
	OnAPIKeyRegenerated(ctx context.Context, oldKey, newKey string)
	OnAPIKeyDeleted(ctx context.Context, rawKey string)
}

// KeyModelAccessProvider exposes per-key model access policy management backed by CPA's key-model-access plugin.
type KeyModelAccessProvider interface {
	GetState(ctx context.Context) (KeyModelAccessState, error)
	UpdateRules(ctx context.Context, apiKeyIDs []int64, allowModels, denyModels []string) (KeyModelAccessState, error)
	Reload(ctx context.Context) (KeyModelAccessState, error)
}

// ComputeKeyModelAccessCallerScope mirrors CPA's built-in API-key caller_scope derivation
// (identical to LTbinglingfeng/key-model-access policy.go).
func ComputeKeyModelAccessCallerScope(rawKey string) string {
	sum := sha256.Sum256([]byte("cli-proxy-api:caller-scope:v1\x00" + strings.TrimSpace(rawKey)))
	return hex.EncodeToString(sum[:])
}

type keyModelAccessService struct {
	db     *gorm.DB
	client *cpa.Client
	mu     sync.Mutex
	logger logrus.FieldLogger

	persistPollAttempts int
	persistPollInterval time.Duration
}

// NewKeyModelAccessService creates a KeyModelAccessProvider backed by Keeper's DB and CPA client.
func NewKeyModelAccessService(db *gorm.DB, client *cpa.Client) *keyModelAccessService {
	return &keyModelAccessService{
		db:                  db,
		client:              client,
		logger:              logrus.StandardLogger(),
		persistPollAttempts: keyModelAccessPersistPollAttempts,
		persistPollInterval: keyModelAccessPersistPollInterval,
	}
}

func (s *keyModelAccessService) GetState(ctx context.Context) (KeyModelAccessState, error) {
	if s == nil || s.client == nil {
		return KeyModelAccessState{Available: false}, nil
	}
	status, _, err := s.client.FetchKeyModelAccessStatus(ctx)
	if err != nil {
		if cpa.IsHTTPStatus(err, http.StatusNotFound) {
			return KeyModelAccessState{Available: false}, nil
		}
		return KeyModelAccessState{}, fmt.Errorf("%w: fetch key-model-access status: %v", ErrCPARequestFailed, err)
	}

	var persistenceSetupError string
	if !status.PersistentUpdates && !status.FailClosed {
		s.mu.Lock()
		nextStatus, initErr := s.ensurePersistenceLocked(ctx, status)
		s.mu.Unlock()
		if initErr != nil {
			persistenceSetupError = initErr.Error()
		} else {
			status = nextStatus
		}
	}

	return s.buildState(ctx, status, persistenceSetupError)
}

func (s *keyModelAccessService) UpdateRules(ctx context.Context, apiKeyIDs []int64, allowModels, denyModels []string) (KeyModelAccessState, error) {
	if s == nil || s.client == nil {
		return KeyModelAccessState{}, ErrKeyModelAccessUnavailable
	}
	ids := uniqueIDs(apiKeyIDs)
	if len(ids) == 0 {
		return KeyModelAccessState{}, fmt.Errorf("%w: at least one api key is required", ErrInvalidInput)
	}
	normalizedAllow, err := normalizeKeyModelAccessPatterns(allowModels, "allowModels")
	if err != nil {
		return KeyModelAccessState{}, err
	}
	normalizedDeny, err := normalizeKeyModelAccessPatterns(denyModels, "denyModels")
	if err != nil {
		return KeyModelAccessState{}, err
	}

	targetScopes := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		if id <= 0 {
			return KeyModelAccessState{}, ErrInvalidID
		}
		row, err := repository.FindActiveCPAAPIKeyByID(s.db, id)
		if err != nil {
			return KeyModelAccessState{}, err
		}
		if row.SourceType == entities.CPAAPIKeySourcePlugin {
			return KeyModelAccessState{}, fmt.Errorf("%w: plugin api keys do not support model access policies", ErrInvalidInput)
		}
		targetScopes[ComputeKeyModelAccessCallerScope(row.APIKey)] = struct{}{}
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	status, _, err := s.client.FetchKeyModelAccessStatus(ctx)
	if err != nil {
		if cpa.IsHTTPStatus(err, http.StatusNotFound) {
			return KeyModelAccessState{}, ErrKeyModelAccessUnavailable
		}
		return KeyModelAccessState{}, fmt.Errorf("%w: fetch key-model-access status: %v", ErrCPARequestFailed, err)
	}

	var persistenceSetupError string
	if !status.PersistentUpdates && !status.FailClosed {
		if nextStatus, initErr := s.ensurePersistenceLocked(ctx, status); initErr != nil {
			persistenceSetupError = initErr.Error()
		} else {
			status = nextStatus
		}
	}

	loaded, etag, _, err := s.client.FetchKeyModelAccessPolicies(ctx)
	if err != nil {
		if cpa.IsHTTPStatus(err, http.StatusNotFound) {
			return KeyModelAccessState{}, ErrKeyModelAccessUnavailable
		}
		return KeyModelAccessState{}, fmt.Errorf("%w: fetch key-model-access policies: %v", ErrCPARequestFailed, err)
	}

	ifMatch := strings.TrimSpace(etag)
	if ifMatch == "" {
		ifMatch = fmt.Sprintf(`"rev-%d"`, loaded.Revision)
	}

	nextDoc := mergeKeyModelAccessDocument(loaded.Policy, targetScopes, normalizedAllow, normalizedDeny)
	if _, _, err := s.client.ReplaceKeyModelAccessPolicies(ctx, nextDoc, ifMatch); err != nil {
		if cpa.IsHTTPStatus(err, http.StatusPreconditionFailed) {
			return KeyModelAccessState{}, ErrKeyModelAccessConflict
		}
		if cpa.IsHTTPStatus(err, http.StatusNotFound) {
			return KeyModelAccessState{}, ErrKeyModelAccessUnavailable
		}
		return KeyModelAccessState{}, fmt.Errorf("%w: replace key-model-access policies: %v", ErrCPARequestFailed, err)
	}

	freshStatus, _, err := s.client.FetchKeyModelAccessStatus(ctx)
	if err == nil {
		status = freshStatus
	}
	return s.buildState(ctx, status, persistenceSetupError)
}

func (s *keyModelAccessService) Reload(ctx context.Context) (KeyModelAccessState, error) {
	if s == nil || s.client == nil {
		return KeyModelAccessState{}, ErrKeyModelAccessUnavailable
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, _, err := s.client.ReloadKeyModelAccessPolicies(ctx); err != nil {
		if cpa.IsHTTPStatus(err, http.StatusNotFound) {
			return KeyModelAccessState{}, ErrKeyModelAccessUnavailable
		}
		if cpa.IsHTTPStatus(err, http.StatusConflict) {
			return KeyModelAccessState{}, ErrKeyModelAccessNotPersistent
		}
		return KeyModelAccessState{}, fmt.Errorf("%w: reload key-model-access policies: %v", ErrCPARequestFailed, err)
	}

	status, _, err := s.client.FetchKeyModelAccessStatus(ctx)
	if err != nil {
		return KeyModelAccessState{}, fmt.Errorf("%w: fetch key-model-access status: %v", ErrCPARequestFailed, err)
	}
	return s.buildState(ctx, status, "")
}

// OnAPIKeyRegenerated migrates an existing key-model-access policy from oldKey's scope to newKey's scope.
// Best-effort: logs a warning on failure and never blocks key regeneration.
func (s *keyModelAccessService) OnAPIKeyRegenerated(ctx context.Context, oldKey, newKey string) {
	if s == nil || s.client == nil {
		return
	}
	oldScope := ComputeKeyModelAccessCallerScope(oldKey)
	newScope := ComputeKeyModelAccessCallerScope(newKey)
	if oldScope == newScope {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	for attempt := 0; attempt < 2; attempt++ {
		loaded, etag, _, err := s.client.FetchKeyModelAccessPolicies(ctx)
		if err != nil {
			if !cpa.IsHTTPStatus(err, http.StatusNotFound) {
				s.logger.WithError(err).Warn("key-model-access: fetch policies during key regeneration failed")
			}
			return
		}
		var moved *cpa.KeyModelAccessPolicyItem
		nextPolicies := make([]cpa.KeyModelAccessPolicyItem, 0, len(loaded.Policy.Policies))
		for _, item := range loaded.Policy.Policies {
			scope := strings.ToLower(strings.TrimSpace(item.CallerScope))
			if scope == oldScope {
				copied := cpa.KeyModelAccessPolicyItem{
					CallerScope: newScope,
					AllowModels: slices.Clone(item.AllowModels),
					DenyModels:  slices.Clone(item.DenyModels),
				}
				moved = &copied
				continue
			}
			if scope == newScope {
				continue
			}
			nextPolicies = append(nextPolicies, item)
		}
		if moved == nil {
			return
		}
		nextPolicies = append(nextPolicies, *moved)
		ifMatch := strings.TrimSpace(etag)
		if ifMatch == "" {
			ifMatch = fmt.Sprintf(`"rev-%d"`, loaded.Revision)
		}
		_, _, err = s.client.ReplaceKeyModelAccessPolicies(ctx, cpa.KeyModelAccessPolicyDocument{
			Version:  keyModelAccessPolicySchemaVersion,
			Policies: nextPolicies,
		}, ifMatch)
		if err == nil {
			return
		}
		if !cpa.IsHTTPStatus(err, http.StatusPreconditionFailed) {
			s.logger.WithError(err).Warn("key-model-access: migrate policy during key regeneration failed")
			return
		}
	}
}

// OnAPIKeyDeleted removes any policy entry belonging to the deleted key.
// Best-effort: logs a warning on failure and never blocks key deletion.
func (s *keyModelAccessService) OnAPIKeyDeleted(ctx context.Context, rawKey string) {
	if s == nil || s.client == nil {
		return
	}
	targetScope := ComputeKeyModelAccessCallerScope(rawKey)
	s.mu.Lock()
	defer s.mu.Unlock()

	for attempt := 0; attempt < 2; attempt++ {
		loaded, etag, _, err := s.client.FetchKeyModelAccessPolicies(ctx)
		if err != nil {
			if !cpa.IsHTTPStatus(err, http.StatusNotFound) {
				s.logger.WithError(err).Warn("key-model-access: fetch policies during key deletion failed")
			}
			return
		}
		found := false
		nextPolicies := make([]cpa.KeyModelAccessPolicyItem, 0, len(loaded.Policy.Policies))
		for _, item := range loaded.Policy.Policies {
			if strings.ToLower(strings.TrimSpace(item.CallerScope)) == targetScope {
				found = true
				continue
			}
			nextPolicies = append(nextPolicies, item)
		}
		if !found {
			return
		}
		ifMatch := strings.TrimSpace(etag)
		if ifMatch == "" {
			ifMatch = fmt.Sprintf(`"rev-%d"`, loaded.Revision)
		}
		_, _, err = s.client.ReplaceKeyModelAccessPolicies(ctx, cpa.KeyModelAccessPolicyDocument{
			Version:  keyModelAccessPolicySchemaVersion,
			Policies: nextPolicies,
		}, ifMatch)
		if err == nil {
			return
		}
		if !cpa.IsHTTPStatus(err, http.StatusPreconditionFailed) {
			s.logger.WithError(err).Warn("key-model-access: prune policy during key deletion failed")
			return
		}
	}
}

// ensurePersistenceLocked mirrors the key-model-access Web UI's default config.toml setup:
// discover plugins_dir -> initialize-storage -> patch plugin config -> poll status.
func (s *keyModelAccessService) ensurePersistenceLocked(ctx context.Context, current cpa.KeyModelAccessStatus) (cpa.KeyModelAccessStatus, error) {
	// Re-check once under lock in case another request already initialized persistence.
	if latest, _, err := s.client.FetchKeyModelAccessStatus(ctx); err == nil {
		current = latest
		if current.PersistentUpdates || current.FailClosed {
			return current, nil
		}
	}

	pluginList, _, err := s.client.FetchManagementPluginsList(ctx)
	if err != nil {
		return current, fmt.Errorf("fetch plugins_dir: %w", err)
	}
	pluginsDir := cpa.DecodePluginsDir(pluginList.PluginsDir)
	if pluginsDir == "" {
		return current, errors.New("CPA did not return a valid plugins_dir")
	}

	initialized, _, err := s.client.InitializeKeyModelAccessStorage(ctx, pluginsDir)
	if err != nil {
		return current, fmt.Errorf("initialize policy file: %w", err)
	}
	policyFile := strings.TrimSpace(initialized.PolicyFile)
	if policyFile == "" {
		return current, errors.New("plugin did not return a default policy file path")
	}

	if _, err := s.client.PatchKeyModelAccessPluginConfig(ctx, policyFile); err != nil {
		return current, fmt.Errorf("save plugin policy_file config: %w", err)
	}

	attempts := s.persistPollAttempts
	if attempts <= 0 {
		attempts = 1
	}
	interval := s.persistPollInterval
	if interval <= 0 {
		interval = 10 * time.Millisecond
	}

	var lastErr error
	for attempt := 0; attempt < attempts; attempt++ {
		if attempt > 0 {
			timer := time.NewTimer(interval)
			select {
			case <-ctx.Done():
				timer.Stop()
				return current, ctx.Err()
			case <-timer.C:
			}
		}
		next, _, err := s.client.FetchKeyModelAccessStatus(ctx)
		if err != nil {
			lastErr = err
			continue
		}
		if next.PersistentUpdates && next.PolicyFile == policyFile {
			return next, nil
		}
	}
	if lastErr != nil {
		return current, lastErr
	}
	return current, errors.New("timed out waiting for policy_file persistence to take effect")
}

func (s *keyModelAccessService) buildState(ctx context.Context, status cpa.KeyModelAccessStatus, persistenceSetupError string) (KeyModelAccessState, error) {
	loadedPolicies, _, _, err := s.client.FetchKeyModelAccessPolicies(ctx)
	if err != nil {
		if cpa.IsHTTPStatus(err, http.StatusNotFound) {
			return KeyModelAccessState{Available: false}, nil
		}
		return KeyModelAccessState{}, fmt.Errorf("%w: fetch key-model-access policies: %v", ErrCPARequestFailed, err)
	}

	models, _, modelsErr := s.client.FetchKeyModelAccessModels(ctx)
	modelsErrText := ""
	if modelsErr != nil {
		models = []string{}
		modelsErrText = modelsErr.Error()
	} else {
		sort.Strings(models)
	}

	keys, err := repository.ListActiveCPAAPIKeys(s.db.WithContext(ctx))
	if err != nil {
		return KeyModelAccessState{}, fmt.Errorf("list active api keys: %w", err)
	}

	byScope := make(map[string]cpa.KeyModelAccessPolicyItem, len(loadedPolicies.Policy.Policies))
	for _, item := range loadedPolicies.Policy.Policies {
		byScope[strings.ToLower(strings.TrimSpace(item.CallerScope))] = item
	}

	mapped := make(map[int64]KeyModelAccessRule)
	for _, row := range keys {
		if row.SourceType == entities.CPAAPIKeySourcePlugin {
			continue
		}
		scope := ComputeKeyModelAccessCallerScope(row.APIKey)
		item, ok := byScope[scope]
		if !ok {
			continue
		}
		allow := nonNilStrings(item.AllowModels)
		deny := nonNilStrings(item.DenyModels)
		if len(allow) == 0 && len(deny) == 0 {
			continue
		}
		mapped[row.ID] = KeyModelAccessRule{
			AllowModels: allow,
			DenyModels:  deny,
		}
	}

	revision := loadedPolicies.Revision
	if status.Revision > revision {
		revision = status.Revision
	}

	return KeyModelAccessState{
		Available: true,
		Status: &KeyModelAccessStatusView{
			Version:               status.Version,
			PolicyCount:           status.PolicyCount,
			Revision:              revision,
			PolicyFile:            status.PolicyFile,
			PersistentUpdates:     status.PersistentUpdates,
			Source:                status.Source,
			UpdatedAt:             status.UpdatedAt,
			FailClosed:            status.FailClosed,
			LastError:             status.LastError,
			PersistenceSetupError: persistenceSetupError,
		},
		Models:      models,
		ModelsError: modelsErrText,
		Policies:    mapped,
	}, nil
}

func mergeKeyModelAccessDocument(
	current cpa.KeyModelAccessPolicyDocument,
	targetScopes map[string]struct{},
	allowModels []string,
	denyModels []string,
) cpa.KeyModelAccessPolicyDocument {
	nextPolicies := make([]cpa.KeyModelAccessPolicyItem, 0, len(current.Policies)+len(targetScopes))
	for _, item := range current.Policies {
		scope := strings.ToLower(strings.TrimSpace(item.CallerScope))
		if _, replacing := targetScopes[scope]; replacing {
			continue
		}
		nextPolicies = append(nextPolicies, cpa.KeyModelAccessPolicyItem{
			CallerScope: scope,
			AllowModels: nonNilStrings(item.AllowModels),
			DenyModels:  nonNilStrings(item.DenyModels),
		})
	}

	if len(allowModels) > 0 || len(denyModels) > 0 {
		orderedScopes := make([]string, 0, len(targetScopes))
		for scope := range targetScopes {
			orderedScopes = append(orderedScopes, scope)
		}
		sort.Strings(orderedScopes)
		for _, scope := range orderedScopes {
			nextPolicies = append(nextPolicies, cpa.KeyModelAccessPolicyItem{
				CallerScope: scope,
				AllowModels: slices.Clone(allowModels),
				DenyModels:  slices.Clone(denyModels),
			})
		}
	}

	return cpa.KeyModelAccessPolicyDocument{
		Version:  keyModelAccessPolicySchemaVersion,
		Policies: nextPolicies,
	}
}

func normalizeKeyModelAccessPatterns(patterns []string, field string) ([]string, error) {
	seen := make(map[string]struct{}, len(patterns))
	result := make([]string, 0, len(patterns))
	for index, raw := range patterns {
		pattern := strings.TrimSpace(raw)
		if pattern == "" {
			return nil, fmt.Errorf("%w: %s[%d] must not be empty", ErrInvalidInput, field, index)
		}
		if len([]rune(pattern)) > keyModelAccessMaxPatternLength {
			return nil, fmt.Errorf("%w: %s[%d] is too long", ErrInvalidInput, field, index)
		}
		for _, r := range pattern {
			if unicode.IsControl(r) {
				return nil, fmt.Errorf("%w: %s[%d] contains control characters", ErrInvalidInput, field, index)
			}
		}
		if _, dup := seen[pattern]; dup {
			continue
		}
		seen[pattern] = struct{}{}
		result = append(result, pattern)
	}
	sort.Strings(result)
	return result, nil
}

func nonNilStrings(values []string) []string {
	if len(values) == 0 {
		return []string{}
	}
	out := make([]string, len(values))
	copy(out, values)
	return out
}
