package test

import (
	"testing"

	"cpa-usage-keeper/internal/entities"
	"cpa-usage-keeper/internal/repository"
)

func TestUpsertAPIKeyQuotaAdjustmentsReplacesSameKeyWindow(t *testing.T) {
	db := openTestDatabase(t)

	if err := repository.UpsertAPIKeyQuotaAdjustments(db, []entities.APIKeyQuotaAdjustment{
		{CPAAPIKeyID: 7, Window: "weekly", WindowKey: "2026-W43", DeltaUSD: 500},
	}); err != nil {
		t.Fatalf("insert adjustment: %v", err)
	}
	if err := repository.UpsertAPIKeyQuotaAdjustments(db, []entities.APIKeyQuotaAdjustment{
		{CPAAPIKeyID: 7, Window: "weekly", WindowKey: "2026-W43", DeltaUSD: -200},
	}); err != nil {
		t.Fatalf("replace adjustment: %v", err)
	}

	var rows []entities.APIKeyQuotaAdjustment
	if err := db.Where("cpa_api_key_id = ? AND window = ?", 7, "weekly").Find(&rows).Error; err != nil {
		t.Fatalf("load adjustments: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected exactly one row for (7, weekly), got %d", len(rows))
	}
	if rows[0].DeltaUSD != -200 {
		t.Fatalf("expected delta -200 after replace, got %v", rows[0].DeltaUSD)
	}
}

func TestListActiveAPIKeyQuotaAdjustmentsFiltersByCurrentWindowKey(t *testing.T) {
	db := openTestDatabase(t)

	if err := repository.UpsertAPIKeyQuotaAdjustments(db, []entities.APIKeyQuotaAdjustment{
		{CPAAPIKeyID: 7, Window: "weekly", WindowKey: "2026-W43", DeltaUSD: 500},
	}); err != nil {
		t.Fatalf("seed adjustment: %v", err)
	}

	rows, err := repository.ListActiveAPIKeyQuotaAdjustments(db, map[string]string{"weekly": "2026-W43"})
	if err != nil {
		t.Fatalf("list active adjustments: %v", err)
	}
	if len(rows) != 1 || rows[0].CPAAPIKeyID != 7 {
		t.Fatalf("expected the active row for 2026-W43, got %+v", rows)
	}

	stale, err := repository.ListActiveAPIKeyQuotaAdjustments(db, map[string]string{"weekly": "2026-W44"})
	if err != nil {
		t.Fatalf("list adjustments for rolled-over window: %v", err)
	}
	if len(stale) != 0 {
		t.Fatalf("expected no active rows for a different window key, got %+v", stale)
	}
}

func TestDeleteAPIKeyQuotaAdjustmentsRemovesOnlyMatchingKeysAndWindows(t *testing.T) {
	db := openTestDatabase(t)

	if err := repository.UpsertAPIKeyQuotaAdjustments(db, []entities.APIKeyQuotaAdjustment{
		{CPAAPIKeyID: 7, Window: "weekly", WindowKey: "2026-W43", DeltaUSD: 500},
		{CPAAPIKeyID: 7, Window: "daily", WindowKey: "2026-10-23", DeltaUSD: 10},
		{CPAAPIKeyID: 9, Window: "weekly", WindowKey: "2026-W43", DeltaUSD: -50},
	}); err != nil {
		t.Fatalf("seed adjustments: %v", err)
	}

	if err := repository.DeleteAPIKeyQuotaAdjustments(db, []int64{7}, []string{"weekly"}); err != nil {
		t.Fatalf("delete adjustments: %v", err)
	}

	var remaining []entities.APIKeyQuotaAdjustment
	if err := db.Order("cpa_api_key_id, window").Find(&remaining).Error; err != nil {
		t.Fatalf("load remaining adjustments: %v", err)
	}
	if len(remaining) != 2 {
		t.Fatalf("expected 2 rows left, got %d: %+v", len(remaining), remaining)
	}
	for _, row := range remaining {
		if row.CPAAPIKeyID == 7 && row.Window == "weekly" {
			t.Fatalf("expected (7, weekly) to be deleted, found %+v", row)
		}
	}
}
