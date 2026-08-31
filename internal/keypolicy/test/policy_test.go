package test

import (
	"testing"

	"cpa-usage-keeper/internal/keypolicy"
)

func TestParseLimitsRoundTripsJSON(t *testing.T) {
	raw := `[{"type":"tokens","window":"daily","value":1000},{"type":"cost","window":"monthly","value":5.5}]`
	limits, err := keypolicy.ParseLimits(raw)
	if err != nil {
		t.Fatalf("parse limits: %v", err)
	}
	if len(limits) != 2 || limits[0].Type != keypolicy.LimitTypeTokens || limits[1].Value != 5.5 {
		t.Fatalf("unexpected limits: %+v", limits)
	}
}

func TestParseLimitsRejectsInvalidJSON(t *testing.T) {
	if _, err := keypolicy.ParseLimits("{not json"); err == nil {
		t.Fatal("expected error for invalid json")
	}
}

func TestLimitsValidateRejectsBadInput(t *testing.T) {
	cases := []keypolicy.Limits{
		{{Type: "bogus", Window: keypolicy.LimitWindowDaily, Value: 1}},
		{{Type: keypolicy.LimitTypeTokens, Window: "yearly", Value: 1}},
		{{Type: keypolicy.LimitTypeTokens, Window: keypolicy.LimitWindowDaily, Value: 0}},
		{{Type: keypolicy.LimitTypeTokens, Window: keypolicy.LimitWindowDaily, Value: -3}},
		{
			{Type: keypolicy.LimitTypeTokens, Window: keypolicy.LimitWindowDaily, Value: 1},
			{Type: keypolicy.LimitTypeTokens, Window: keypolicy.LimitWindowDaily, Value: 2},
		},
	}
	for i, limits := range cases {
		if err := limits.Validate(); err == nil {
			t.Fatalf("case %d: expected validation error for %+v", i, limits)
		}
	}
}

func TestLimitsValidateAcceptsGoodInput(t *testing.T) {
	limits := keypolicy.Limits{
		{Type: keypolicy.LimitTypeRequests, Window: keypolicy.LimitWindowDaily, Value: 10},
		{Type: keypolicy.LimitTypeRequests, Window: keypolicy.LimitWindowMonthly, Value: 300},
		{Type: keypolicy.LimitTypeCost, Window: keypolicy.LimitWindowMonthly, Value: 9.9},
	}
	if err := limits.Validate(); err != nil {
		t.Fatalf("expected valid: %v", err)
	}
}
