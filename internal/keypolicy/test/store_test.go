package test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"cpa-usage-keeper/internal/config"
	"cpa-usage-keeper/internal/entities"
	"cpa-usage-keeper/internal/keypolicy"
	"cpa-usage-keeper/internal/pricing"
	"cpa-usage-keeper/internal/repository"
	"cpa-usage-keeper/internal/repository/migration"

	"gorm.io/gorm"
)

// openTestDB 复用 repository 测试的全新库打开路径：AutoMigrate 当前 schema 并把全部迁移标记为已应用。
// internal/repository/test 只有 _test.go 文件，无法被其他包导入，因此在这里复制它的打开方式。
func openTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := repository.OpenDatabase(config.Config{SQLitePath: filepath.Join(t.TempDir(), "app.db")})
	if err != nil {
		t.Fatalf("open test database: %v", err)
	}
	t.Cleanup(func() {
		if sqlDB, err := db.DB(); err == nil {
			_ = sqlDB.Close()
		}
	})
	return db
}

func newKeypolicyTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	// 复用 repository 测试的打开方式：全新 SQLite 库 + 当前完整 schema。
	db := openTestDB(t)
	// 全新库已标记全部迁移 applied，这里再跑一次 Run 作为幂等兜底。
	if err := migration.Run(db); err != nil {
		t.Fatalf("run migrations: %v", err)
	}
	return db
}

// newSnapshotForTest 用 pricing 真实暴露的 CompileSnapshot 构造价格快照。
// 简报猜测的字段 InputPricePerMToken 并不存在，输入侧按百万 token 计价的字段是 PromptPricePer1M。
func newSnapshotForTest(t *testing.T, settings map[string]entities.ModelPriceSetting) *pricing.Snapshot {
	t.Helper()
	configs := make([]pricing.ModelConfig, 0, len(settings))
	for _, setting := range settings {
		configs = append(configs, pricing.ModelConfig{Pricing: setting})
	}
	snapshot, err := pricing.CompileSnapshot(configs)
	if err != nil {
		t.Fatalf("compile pricing snapshot: %v", err)
	}
	return snapshot
}

// newEmptySnapshotForTest 直接复用 pricing 暴露的只读空价格快照。
func newEmptySnapshotForTest(t *testing.T) *pricing.Snapshot {
	t.Helper()
	return pricing.EmptySnapshot()
}

func TestPerKeyUsageAggregatesThreeWindowsAndPricesCost(t *testing.T) {
	db := newKeypolicyTestDB(t)
	key := entities.CPAAPIKey{APIKey: "sk-agg", DisplayKey: "sk-agg"}
	if err := db.Create(&key).Error; err != nil {
		t.Fatalf("seed key: %v", err)
	}
	// 三个窗口从固定的周三构造，事件时间戳全部确定：
	// 日窗口 09-16、周窗口 09-14~09-21、月窗口 09-01~10-01。
	anchor := time.Date(2026, 9, 16, 15, 0, 0, 0, time.Local)
	daily := keypolicy.DailyWindow(anchor)
	weekly := keypolicy.WeeklyWindow(anchor)
	monthly := keypolicy.MonthlyWindow(anchor)
	insertEvent := func(ts time.Time, tokens int64) {
		event := entities.UsageEvent{
			APIGroupKey: "sk-agg", Model: "gpt-5",
			InputTokens: tokens, OutputTokens: 0, TotalTokens: tokens,
			Timestamp: ts,
		}
		if err := db.Create(&event).Error; err != nil {
			t.Fatalf("seed event: %v", err)
		}
	}
	insertEvent(time.Date(2026, 9, 16, 12, 0, 0, 0, time.Local), 100) // 三个窗口都命中
	insertEvent(time.Date(2026, 9, 15, 12, 0, 0, 0, time.Local), 50)  // 周 + 月
	insertEvent(time.Date(2026, 9, 2, 12, 0, 0, 0, time.Local), 200)  // 仅月
	insertEvent(time.Date(2026, 8, 20, 12, 0, 0, 0, time.Local), 999) // 全部窗口之外

	catalog := pricing.NewCatalog(newSnapshotForTest(t, map[string]entities.ModelPriceSetting{
		"gpt-5": {Model: "gpt-5", PromptPricePer1M: 1},
	}))
	store := keypolicy.NewStore(db, catalog)
	usage, err := store.PerKeyUsage(context.Background(), daily, weekly, monthly)
	if err != nil {
		t.Fatalf("per key usage: %v", err)
	}
	got := usage[key.ID]
	assertCost := func(window keypolicy.LimitWindow, want float64) {
		t.Helper()
		if diff := got[window].CostUSD - want; diff > 1e-12 || diff < -1e-12 {
			t.Fatalf("%s cost mismatch: got %v want %v", window, got[window].CostUSD, want)
		}
	}
	// gpt-5 @1/M：100 tokens = 0.0001，150 = 0.00015，350 = 0.00035。
	assertCost(keypolicy.LimitWindowDaily, 0.0001)
	assertCost(keypolicy.LimitWindowWeekly, 0.00015)
	assertCost(keypolicy.LimitWindowMonthly, 0.00035)
}

func TestPerKeyUsageUnknownModelCostsZero(t *testing.T) {
	db := newKeypolicyTestDB(t)
	key := entities.CPAAPIKey{APIKey: "sk-unpriced", DisplayKey: "sk-unpriced"}
	if err := db.Create(&key).Error; err != nil {
		t.Fatalf("seed key: %v", err)
	}
	now := time.Now()
	event := entities.UsageEvent{APIGroupKey: "sk-unpriced", Model: "mystery", InputTokens: 500, TotalTokens: 500, Timestamp: now.Add(-time.Minute)}
	if err := db.Create(&event).Error; err != nil {
		t.Fatalf("seed event: %v", err)
	}
	store := keypolicy.NewStore(db, pricing.NewCatalog(newEmptySnapshotForTest(t)))
	usage, err := store.PerKeyUsage(context.Background(), keypolicy.DailyWindow(now), keypolicy.WeeklyWindow(now), keypolicy.MonthlyWindow(now))
	if err != nil {
		t.Fatalf("per key usage: %v", err)
	}
	if usage[key.ID][keypolicy.LimitWindowDaily].CostUSD != 0 {
		t.Fatalf("expected zero cost for unpriced model, got %v", usage[key.ID][keypolicy.LimitWindowDaily].CostUSD)
	}
}
