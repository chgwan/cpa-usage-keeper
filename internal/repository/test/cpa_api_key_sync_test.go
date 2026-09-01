package test

import (
	"testing"
	"time"

	"cpa-usage-keeper/internal/entities"
	"cpa-usage-keeper/internal/repository"
)

// TestSyncCPAAPIKeysExcludesPolicyHeldKeysFromStaleness 验证被策略禁用的 key 缺席 CPA 时
// 不会被 sync 误标删除，而没有策略或策略仍为 active 的 key 保持原有过期删除语义。
func TestSyncCPAAPIKeysExcludesPolicyHeldKeysFromStaleness(t *testing.T) {
	cases := []struct {
		name string
		// state 为空表示不建策略行。
		state       string
		wantActive  bool
		description string
	}{
		// 禁用（超限）状态是被有意移出 CPA，本地行必须保留。
		{name: "disabled_by_quota", state: "disabled_by_quota", wantActive: true, description: "quota-disabled key stays active"},
		// 手动禁用同样是有意缺席。
		{name: "disabled_manual", state: "disabled_manual", wantActive: true, description: "manually disabled key stays active"},
		// 策略仍为 active 却不在 CPA 里，才是真正的过期。
		{name: "active", state: "active", wantActive: false, description: "active policy still marked deleted"},
		// 没有策略行的 key 维持原有删除行为。
		{name: "no policy", state: "", wantActive: false, description: "policy-less key still marked deleted"},
	}
	for _, tc := range cases {
		t.Run(tc.description, func(t *testing.T) {
			db := openTestDatabase(t)
			key := entities.CPAAPIKey{APIKey: "sk-held", DisplayKey: "sk-held"}
			if err := db.Create(&key).Error; err != nil {
				t.Fatalf("seed key: %v", err)
			}
			if tc.state != "" {
				policy := &entities.CPAAPIKeyPolicy{
					CPAAPIKeyID: key.ID, Limits: "[]", Enabled: true, EnforcementState: tc.state,
				}
				if err := repository.UpsertCPAAPIKeyPolicy(db, policy); err != nil {
					t.Fatalf("seed policy: %v", err)
				}
			}
			// CPA 列表只剩旁观 key，模拟禁用后的周期 metadata sync。
			if err := repository.SyncCPAAPIKeys(db, []string{"sk-bystander"}, time.Now()); err != nil {
				t.Fatalf("sync api keys: %v", err)
			}
			_, err := repository.FindActiveCPAAPIKeyByID(db, key.ID)
			if tc.wantActive && err != nil {
				t.Fatalf("expected policy-held row to stay active, got %v", err)
			}
			if !tc.wantActive && err == nil {
				t.Fatal("expected stale row to be marked deleted")
			}
		})
	}
}
