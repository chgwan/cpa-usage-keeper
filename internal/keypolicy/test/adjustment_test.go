package test

import (
	"testing"

	"cpa-usage-keeper/internal/keypolicy"
)

func TestApplyAdjustmentsAddsSignedDeltaAndFloorsAtZero(t *testing.T) {
	limits := keypolicy.Limits{
		{Type: keypolicy.LimitTypeCost, Window: keypolicy.LimitWindowDaily, Value: 10},
		{Type: keypolicy.LimitTypeCost, Window: keypolicy.LimitWindowWeekly, Value: 1600},
		{Type: keypolicy.LimitTypeCost, Window: keypolicy.LimitWindowMonthly, Value: 100},
	}
	got := keypolicy.ApplyAdjustments(limits, keypolicy.Adjustments{
		keypolicy.LimitWindowWeekly:  500,
		keypolicy.LimitWindowMonthly: -250,
	})
	if len(got) != 3 {
		t.Fatalf("want 3 limits, got %d", len(got))
	}
	if got[0].Value != 10 || got[0].Base != 10 || got[0].Adjustment != nil {
		t.Fatalf("daily untouched: %+v", got[0])
	}
	if got[1].Value != 2100 || got[1].Base != 1600 || got[1].Adjustment == nil || *got[1].Adjustment != 500 {
		t.Fatalf("weekly: %+v", got[1])
	}
	if got[2].Value != 0 || got[2].Base != 100 || *got[2].Adjustment != -250 {
		t.Fatalf("monthly floors at 0: %+v", got[2])
	}
	if limits[1].Value != 1600 {
		t.Fatalf("input mutated")
	}
}

func TestApplyAdjustmentsIgnoresWindowsWithoutNormalLimit(t *testing.T) {
	limits := keypolicy.Limits{{Type: keypolicy.LimitTypeCost, Window: keypolicy.LimitWindowWeekly, Value: 50}}
	got := keypolicy.ApplyAdjustments(limits, keypolicy.Adjustments{keypolicy.LimitWindowDaily: 99})
	if len(got) != 1 || got[0].Window != keypolicy.LimitWindowWeekly || got[0].Value != 50 {
		t.Fatalf("got %+v", got)
	}
}

func TestZeroEffectiveLimitAlwaysBreachesAndReportsFullRatio(t *testing.T) {
	limits := keypolicy.ApplyAdjustments(
		keypolicy.Limits{{Type: keypolicy.LimitTypeCost, Window: keypolicy.LimitWindowWeekly, Value: 100}},
		keypolicy.Adjustments{keypolicy.LimitWindowWeekly: -100},
	)
	usage := keypolicy.UsageByWindow{keypolicy.LimitWindowWeekly: {CostUSD: 0}}
	if keypolicy.Evaluate(limits.Limits(), usage) == nil {
		t.Fatalf("zero limit must breach even with zero usage")
	}
	tightest := limits.Tightest(usage)
	if tightest == nil || tightest.Ratio != 1 || tightest.Base != 100 {
		t.Fatalf("tightest: %+v", tightest)
	}
	rows := limits.WindowUsages(usage)
	if len(rows) != 1 || rows[0].Ratio != 1 {
		t.Fatalf("window usages: %+v", rows)
	}
}
