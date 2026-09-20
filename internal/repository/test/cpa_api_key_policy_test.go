package test

import (
	"testing"
	"time"

	"cpa-usage-keeper/internal/entities"
	"cpa-usage-keeper/internal/repository"
)

func TestUpsertCPAAPIKeyPolicyInsertsThenUpdates(t *testing.T) {
	db := openTestDatabase(t)
	key := entities.CPAAPIKey{APIKey: "sk-a", DisplayKey: "sk-a"}
	if err := db.Create(&key).Error; err != nil {
		t.Fatalf("seed key: %v", err)
	}
	policy := &entities.CPAAPIKeyPolicy{CPAAPIKeyID: key.ID, Limits: "[]", Enabled: true, EnforcementState: "active"}
	if err := repository.UpsertCPAAPIKeyPolicy(db, policy); err != nil {
		t.Fatalf("insert policy: %v", err)
	}
	policy.EnforcementState = "disabled_by_quota"
	policy.Limits = `[{"type":"tokens","window":"daily","value":100}]`
	if err := repository.UpsertCPAAPIKeyPolicy(db, policy); err != nil {
		t.Fatalf("update policy: %v", err)
	}
	loaded, err := repository.FindCPAAPIKeyPolicy(db, key.ID)
	if err != nil {
		t.Fatalf("find policy: %v", err)
	}
	if loaded.EnforcementState != "disabled_by_quota" || loaded.Limits == "[]" {
		t.Fatalf("expected updated policy, got %+v", loaded)
	}
}

func TestListEnabledCPAAPIKeyPoliciesJoinsKeyValue(t *testing.T) {
	db := openTestDatabase(t)
	key := entities.CPAAPIKey{APIKey: "sk-b", DisplayKey: "sk-b", KeyAlias: "alias-b"}
	if err := db.Create(&key).Error; err != nil {
		t.Fatalf("seed key: %v", err)
	}
	if err := repository.UpsertCPAAPIKeyPolicy(db, &entities.CPAAPIKeyPolicy{CPAAPIKeyID: key.ID, Limits: "[]", Enabled: true}); err != nil {
		t.Fatalf("seed policy: %v", err)
	}
	if err := repository.UpsertCPAAPIKeyPolicy(db, &entities.CPAAPIKeyPolicy{CPAAPIKeyID: key.ID + 1, Limits: "[]", Enabled: false}); err != nil {
		t.Fatalf("seed disabled policy: %v", err)
	}
	rows, err := repository.ListEnabledCPAAPIKeyPolicies(db)
	if err != nil {
		t.Fatalf("list policies: %v", err)
	}
	if len(rows) != 1 || rows[0].APIKey != "sk-b" || rows[0].KeyAlias != "alias-b" {
		t.Fatalf("expected single joined row, got %+v", rows)
	}
}

func TestEnforcementLogInsertListAndLatest(t *testing.T) {
	db := openTestDatabase(t)
	now := time.Now()
	first := entities.APIKeyEnforcementLog{CPAAPIKeyID: 7, Action: "disabled", Reason: "limit_breached", CreatedAt: now.Add(-time.Minute)}
	second := entities.APIKeyEnforcementLog{CPAAPIKeyID: 7, Action: "restored", Reason: "window_reset", CreatedAt: now}
	if err := repository.InsertAPIKeyEnforcementLog(db, first); err != nil {
		t.Fatalf("insert first log: %v", err)
	}
	if err := repository.InsertAPIKeyEnforcementLog(db, second); err != nil {
		t.Fatalf("insert second log: %v", err)
	}
	logs, err := repository.ListAPIKeyEnforcementLogs(db, 7, 10)
	if err != nil || len(logs) != 2 {
		t.Fatalf("expected 2 logs, got %d err %v", len(logs), err)
	}
	if !logs[0].CreatedAt.After(logs[1].CreatedAt) {
		t.Fatal("expected newest-first ordering")
	}
	latest, err := repository.LatestAPIKeyEnforcementLogAction(db, 7, "disabled")
	if err != nil || latest.Reason != "limit_breached" {
		t.Fatalf("expected latest disabled log, got %+v err %v", latest, err)
	}
}

func TestUpdateCPAAPIKeyValueRewritesKeyString(t *testing.T) {
	db := openTestDatabase(t)
	key := entities.CPAAPIKey{APIKey: "sk-old", DisplayKey: "sk-old", KeyAlias: "keep-me"}
	if err := db.Create(&key).Error; err != nil {
		t.Fatalf("seed key: %v", err)
	}
	if err := repository.UpdateCPAAPIKeyValue(db, key.ID, "sk-new"); err != nil {
		t.Fatalf("update key value: %v", err)
	}
	loaded, err := repository.FindActiveCPAAPIKeyByID(db, key.ID)
	if err != nil {
		t.Fatalf("reload key: %v", err)
	}
	if loaded.APIKey != "sk-new" || loaded.KeyAlias != "keep-me" {
		t.Fatalf("expected new value with alias kept, got %+v", loaded)
	}
}
