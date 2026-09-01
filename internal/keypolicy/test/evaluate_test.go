package test

import (
	"testing"
	"time"

	"cpa-usage-keeper/internal/keypolicy"
)

func TestDailyWindowUsesLocalCalendar(t *testing.T) {
	loc := time.FixedZone("test", 8*3600)
	now := time.Date(2026, 8, 31, 15, 4, 5, 0, loc)
	w := keypolicy.DailyWindow(now)
	if !w.Start.Equal(time.Date(2026, 8, 31, 0, 0, 0, 0, loc)) {
		t.Fatalf("expected local midnight start, got %v", w.Start)
	}
	if !w.End.Equal(time.Date(2026, 9, 1, 0, 0, 0, 0, loc)) {
		t.Fatalf("expected next local midnight end, got %v", w.End)
	}
	if keypolicy.WindowKey(w) != "2026-08-31" {
		t.Fatalf("expected date window key, got %q", keypolicy.WindowKey(w))
	}
}

func TestMonthlyWindowUsesLocalCalendar(t *testing.T) {
	loc := time.FixedZone("test", 8*3600)
	now := time.Date(2026, 8, 31, 23, 59, 59, 0, loc)
	w := keypolicy.MonthlyWindow(now)
	if keypolicy.WindowKey(w) != "2026-08" {
		t.Fatalf("expected month window key, got %q", keypolicy.WindowKey(w))
	}
	if !w.End.Equal(time.Date(2026, 9, 1, 0, 0, 0, 0, loc)) {
		t.Fatalf("expected month end at next month start, got %v", w.End)
	}
}

func sampleUsage() keypolicy.UsageByWindow {
	return keypolicy.UsageByWindow{
		keypolicy.LimitWindowDaily:   {Requests: 5, Tokens: 900, CostUSD: 0.4},
		keypolicy.LimitWindowMonthly: {Requests: 200, Tokens: 50000, CostUSD: 20},
	}
}

func TestEvaluateReturnsNilWhenUnderAllLimits(t *testing.T) {
	limits := keypolicy.Limits{
		{Type: keypolicy.LimitTypeRequests, Window: keypolicy.LimitWindowDaily, Value: 10},
		{Type: keypolicy.LimitTypeTokens, Window: keypolicy.LimitWindowDaily, Value: 5000},
	}
	if breach := keypolicy.Evaluate(limits, sampleUsage()); breach != nil {
		t.Fatalf("expected no breach, got %+v", breach)
	}
}

func TestEvaluateDetectsEachDimensionAtLimit(t *testing.T) {
	cases := []struct {
		limit  keypolicy.Limit
		used   float64
		wanted keypolicy.LimitType
	}{
		{keypolicy.Limit{Type: keypolicy.LimitTypeRequests, Window: keypolicy.LimitWindowDaily, Value: 5}, 5, keypolicy.LimitTypeRequests},
		{keypolicy.Limit{Type: keypolicy.LimitTypeTokens, Window: keypolicy.LimitWindowDaily, Value: 900}, 900, keypolicy.LimitTypeTokens},
		{keypolicy.Limit{Type: keypolicy.LimitTypeCost, Window: keypolicy.LimitWindowMonthly, Value: 20}, 20, keypolicy.LimitTypeCost},
	}
	for i, tc := range cases {
		breach := keypolicy.Evaluate(keypolicy.Limits{tc.limit}, sampleUsage())
		if breach == nil {
			t.Fatalf("case %d: expected breach", i)
		}
		if breach.Limit.Type != tc.wanted || breach.Used != tc.used {
			t.Fatalf("case %d: unexpected breach %+v", i, breach)
		}
	}
}

func TestEvaluateFirstBreachWinsInDeclarationOrder(t *testing.T) {
	limits := keypolicy.Limits{
		{Type: keypolicy.LimitTypeTokens, Window: keypolicy.LimitWindowMonthly, Value: 10},
		{Type: keypolicy.LimitTypeRequests, Window: keypolicy.LimitWindowDaily, Value: 1},
	}
	breach := keypolicy.Evaluate(limits, sampleUsage())
	if breach == nil || breach.Limit.Type != keypolicy.LimitTypeTokens {
		t.Fatalf("expected first declared breach to win, got %+v", breach)
	}
}

func TestEvaluateMissingWindowCountsAsZero(t *testing.T) {
	limits := keypolicy.Limits{{Type: keypolicy.LimitTypeTokens, Window: keypolicy.LimitWindowMonthly, Value: 1}}
	if breach := keypolicy.Evaluate(limits, nil); breach != nil {
		t.Fatalf("expected zero usage to stay under limit, got %+v", breach)
	}
}

func TestTightestLimitPicksHighestRatio(t *testing.T) {
	limits := keypolicy.Limits{
		{Type: keypolicy.LimitTypeRequests, Window: keypolicy.LimitWindowDaily, Value: 10},
		{Type: keypolicy.LimitTypeTokens, Window: keypolicy.LimitWindowDaily, Value: 1000},
	}
	tightest := limits.Tightest(sampleUsage())
	if tightest == nil || tightest.Limit.Type != keypolicy.LimitTypeTokens || tightest.Ratio != 0.9 {
		t.Fatalf("expected tokens at 0.9, got %+v", tightest)
	}
}
