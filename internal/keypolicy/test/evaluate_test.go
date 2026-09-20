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

func TestWeeklyWindowStartsMondayLocalMidnight(t *testing.T) {
	loc := time.FixedZone("test", 8*3600)
	// 2026-09-16 是周三：所在周从周一 2026-09-14 起到下周一 2026-09-21 止。
	now := time.Date(2026, 9, 16, 15, 4, 5, 0, loc)
	w := keypolicy.WeeklyWindow(now)
	if !w.Start.Equal(time.Date(2026, 9, 14, 0, 0, 0, 0, loc)) {
		t.Fatalf("expected Monday midnight start, got %v", w.Start)
	}
	if !w.End.Equal(time.Date(2026, 9, 21, 0, 0, 0, 0, loc)) {
		t.Fatalf("expected next Monday midnight end, got %v", w.End)
	}
	if keypolicy.WindowKey(w) != "2026-W38" {
		t.Fatalf("expected ISO week window key, got %q", keypolicy.WindowKey(w))
	}
}

func TestWeeklyWindowSundayBelongsToPrecedingMonday(t *testing.T) {
	loc := time.FixedZone("test", 8*3600)
	// 周日晚归属本周（周一开始的那一周），而不是下一周。
	now := time.Date(2026, 9, 20, 23, 0, 0, 0, loc)
	w := keypolicy.WeeklyWindow(now)
	if !w.Start.Equal(time.Date(2026, 9, 14, 0, 0, 0, 0, loc)) {
		t.Fatalf("expected preceding Monday start, got %v", w.Start)
	}
}

func TestWeeklyWindowMondayStartsSameDay(t *testing.T) {
	loc := time.FixedZone("test", 8*3600)
	now := time.Date(2026, 9, 14, 0, 30, 0, 0, loc)
	w := keypolicy.WeeklyWindow(now)
	if !w.Start.Equal(time.Date(2026, 9, 14, 0, 0, 0, 0, loc)) {
		t.Fatalf("expected same-day Monday start, got %v", w.Start)
	}
}

func sampleUsage() keypolicy.UsageByWindow {
	return keypolicy.UsageByWindow{
		keypolicy.LimitWindowDaily:   {CostUSD: 0.4},
		keypolicy.LimitWindowWeekly:  {CostUSD: 5},
		keypolicy.LimitWindowMonthly: {CostUSD: 20},
	}
}

func TestEvaluateReturnsNilWhenUnderAllLimits(t *testing.T) {
	limits := keypolicy.Limits{
		{Type: keypolicy.LimitTypeCost, Window: keypolicy.LimitWindowDaily, Value: 10},
		{Type: keypolicy.LimitTypeCost, Window: keypolicy.LimitWindowWeekly, Value: 100},
	}
	if breach := keypolicy.Evaluate(limits, sampleUsage()); breach != nil {
		t.Fatalf("expected no breach, got %+v", breach)
	}
}

func TestEvaluateDetectsEachWindowAtLimit(t *testing.T) {
	cases := []struct {
		limit keypolicy.Limit
		used  float64
	}{
		{keypolicy.Limit{Type: keypolicy.LimitTypeCost, Window: keypolicy.LimitWindowDaily, Value: 0.4}, 0.4},
		{keypolicy.Limit{Type: keypolicy.LimitTypeCost, Window: keypolicy.LimitWindowWeekly, Value: 5}, 5},
		{keypolicy.Limit{Type: keypolicy.LimitTypeCost, Window: keypolicy.LimitWindowMonthly, Value: 20}, 20},
	}
	for i, tc := range cases {
		breach := keypolicy.Evaluate(keypolicy.Limits{tc.limit}, sampleUsage())
		if breach == nil {
			t.Fatalf("case %d: expected breach", i)
		}
		if breach.Limit.Window != tc.limit.Window || breach.Used != tc.used {
			t.Fatalf("case %d: unexpected breach %+v", i, breach)
		}
	}
}

func TestEvaluateFirstBreachWinsInDeclarationOrder(t *testing.T) {
	limits := keypolicy.Limits{
		{Type: keypolicy.LimitTypeCost, Window: keypolicy.LimitWindowMonthly, Value: 10},
		{Type: keypolicy.LimitTypeCost, Window: keypolicy.LimitWindowDaily, Value: 0.1},
	}
	breach := keypolicy.Evaluate(limits, sampleUsage())
	if breach == nil || breach.Limit.Window != keypolicy.LimitWindowMonthly {
		t.Fatalf("expected first declared breach to win, got %+v", breach)
	}
}

func TestEvaluateMissingWindowCountsAsZero(t *testing.T) {
	limits := keypolicy.Limits{{Type: keypolicy.LimitTypeCost, Window: keypolicy.LimitWindowMonthly, Value: 1}}
	if breach := keypolicy.Evaluate(limits, nil); breach != nil {
		t.Fatalf("expected zero usage to stay under limit, got %+v", breach)
	}
}

func TestTightestLimitPicksHighestRatio(t *testing.T) {
	limits := keypolicy.Limits{
		{Type: keypolicy.LimitTypeCost, Window: keypolicy.LimitWindowDaily, Value: 1},
		{Type: keypolicy.LimitTypeCost, Window: keypolicy.LimitWindowWeekly, Value: 5},
	}
	tightest := limits.Tightest(sampleUsage())
	if tightest == nil || tightest.Limit.Window != keypolicy.LimitWindowWeekly || tightest.Ratio != 1 {
		t.Fatalf("expected weekly at ratio 1, got %+v", tightest)
	}
}
