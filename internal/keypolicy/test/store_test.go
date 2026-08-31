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

func TestPerKeyUsageAggregatesWindowsAndPricesCost(t *testing.T) {
	db := newKeypolicyTestDB(t)
	key := entities.CPAAPIKey{APIKey: "sk-agg", DisplayKey: "sk-agg"}
	if err := db.Create(&key).Error; err != nil {
		t.Fatalf("seed key: %v", err)
	}
	now := time.Now()
	// 固定取当日正午，保证事件一定落在日窗口内（简报的 now-1h 在本地 0 点后会跨到昨天）。
	today := time.Date(now.Year(), now.Month(), now.Day(), 12, 0, 0, 0, now.Location())
	thisMonth := time.Date(now.Year(), now.Month(), 1, 12, 0, 0, 0, now.Location())
	monthlySeed := thisMonth
	if now.Day() == 1 {
		monthlySeed = thisMonth.AddDate(0, 0, 1) // 1 号时与 today 重叠，顺延一天避开日窗口
	}
	insertEvent := func(ts time.Time, model string, tokens int64) {
		event := entities.UsageEvent{
			APIGroupKey: "sk-agg", Model: model,
			InputTokens: tokens, OutputTokens: 0, TotalTokens: tokens,
			Timestamp: ts,
		}
		if err := db.Create(&event).Error; err != nil {
			t.Fatalf("seed event: %v", err)
		}
	}
	insertEvent(today, "gpt-5", 100)
	insertEvent(monthlySeed, "claude-x", 50)
	insertEvent(thisMonth.AddDate(0, 0, -40), "old-beyond-month", 999) // 月窗口外

	catalog := pricing.NewCatalog(newSnapshotForTest(t, map[string]entities.ModelPriceSetting{
		"gpt-5":    {Model: "gpt-5", PromptPricePer1M: 1},
		"claude-x": {Model: "claude-x", PromptPricePer1M: 2},
	}))
	store := keypolicy.NewStore(db, catalog)
	daily := keypolicy.DailyWindow(now)
	monthly := keypolicy.MonthlyWindow(now)
	usage, err := store.PerKeyUsage(context.Background(), daily, monthly)
	if err != nil {
		t.Fatalf("per key usage: %v", err)
	}
	got := usage[key.ID]
	if got[keypolicy.LimitWindowDaily].Requests != 1 || got[keypolicy.LimitWindowDaily].Tokens != 100 {
		t.Fatalf("daily usage mismatch: %+v", got[keypolicy.LimitWindowDaily])
	}
	if got[keypolicy.LimitWindowMonthly].Requests != 2 || got[keypolicy.LimitWindowMonthly].Tokens != 150 {
		t.Fatalf("monthly usage mismatch: %+v", got[keypolicy.LimitWindowMonthly])
	}
	// gpt-5 100 tokens @ 1/M = 0.0001；claude-x 50 tokens @ 2/M = 0.0001。
	if diff := got[keypolicy.LimitWindowMonthly].CostUSD - 0.0002; diff > 1e-12 || diff < -1e-12 {
		t.Fatalf("monthly cost mismatch: %v", got[keypolicy.LimitWindowMonthly].CostUSD)
	}
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
	usage, err := store.PerKeyUsage(context.Background(), keypolicy.DailyWindow(now), keypolicy.MonthlyWindow(now))
	if err != nil {
		t.Fatalf("per key usage: %v", err)
	}
	if usage[key.ID][keypolicy.LimitWindowDaily].CostUSD != 0 {
		t.Fatalf("expected zero cost for unpriced model, got %v", usage[key.ID][keypolicy.LimitWindowDaily].CostUSD)
	}
}
