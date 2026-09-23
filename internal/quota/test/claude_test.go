package test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"cpa-usage-keeper/internal/cpa/dto/apicall"
	"cpa-usage-keeper/internal/entities"
	"cpa-usage-keeper/internal/quota"
)

func TestClaudeProviderCallsUsageAndProfile(t *testing.T) {
	caller := &recordingManagementCaller{responses: []*apicall.Response{
		quotaAPIResponse(200, `{"five_hour":{"utilization":36,"resets_at":"2026-05-09T12:00:00Z"},"seven_day":{"utilization":72,"resets_at":"2026-05-10T12:00:00Z"},"seven_day_sonnet":{"utilization":18,"resets_at":"2026-05-10T08:00:00Z"},"extra_usage":{"is_enabled":true,"monthly_limit":1000,"used_credits":250,"utilization":25}}`),
		quotaAPIResponse(200, `{"account":{"email":"user@example.com","has_claude_pro":true},"organization":{"organization_type":"claude_team","subscription_status":"active"}}`),
	}}
	configs := quota.DefaultProviderConfigs()
	provider := quota.NewClaudeProvider(caller, configs.ClaudeUsage, configs.ClaudeProfile)

	output, err := provider.Check(context.Background(), quota.ProviderInput{Identity: entities.UsageIdentity{Identity: "claude-auth"}})
	if err != nil {
		t.Fatalf("Check returned error: %v", err)
	}
	if output.Provider != "claude" {
		t.Fatalf("expected claude output provider, got %q", output.Provider)
	}
	if len(caller.requests) != 2 {
		t.Fatalf("expected two api-call requests, got %d", len(caller.requests))
	}
	for index, path := range []string{"usage", "profile"} {
		request := caller.requests[index]
		if request.AuthIndex != "claude-auth" || request.Method != "GET" || request.URL != "https://api.anthropic.com/api/oauth/"+path {
			t.Fatalf("unexpected %s request: %+v", path, request)
		}
		if request.Header["Authorization"] != "Bearer $TOKEN$" || request.Header["Content-Type"] != "application/json" || request.Header["anthropic-beta"] != "oauth-2025-04-20" {
			t.Fatalf("unexpected %s headers: %+v", path, request.Header)
		}
	}

	result, ok := output.Result.(quota.ClaudeResult)
	if !ok {
		t.Fatalf("expected claude result type, got %T", output.Result)
	}
	if result.Usage == nil || result.Usage.FiveHour == nil || result.Usage.FiveHour.Utilization != 36 || result.Usage.SevenDay == nil || result.Usage.SevenDay.Utilization != 72 || result.Usage.SevenDaySonnet == nil || result.Usage.SevenDaySonnet.Utilization != 18 || result.Usage.ExtraUsage == nil || result.Usage.ExtraUsage.UsedCredits != 250 || result.Usage.ExtraUsage.Utilization == nil || *result.Usage.ExtraUsage.Utilization != 25 {
		t.Fatalf("expected parsed claude usage payload, got %#v", result.Usage)
	}
	if result.Profile == nil || result.Profile.Account == nil || result.Profile.Account.Email != "user@example.com" || result.Profile.Account.HasClaudePro == nil || !*result.Profile.Account.HasClaudePro {
		t.Fatalf("expected parsed claude profile payload, got %#v", result.Profile)
	}
	encoded, err := json.Marshal(output.Result)
	if err != nil {
		t.Fatalf("marshal claude result: %v", err)
	}
	body := string(encoded)
	if !contains(body, `"usage":{"fiveHour"`) || !contains(body, `"profile":{"account"`) || contains(body, "bodyText") || contains(body, "statusCode") {
		t.Fatalf("unexpected claude result JSON: %s", body)
	}
}

func TestClaudeProviderParsesScopedModelLimits(t *testing.T) {
	usageBody := `{"five_hour":{"utilization":36,"resets_at":"2026-05-09T12:00:00Z"},"iguana_necktie":{"utilization":61,"resets_at":"2026-05-01T12:00:00Z"},"limits":[{"kind":"weekly_scoped","scope":{"model":{"display_name":"Fable 5"}},"percent":44.5,"resets_at":"2026-05-14T12:00:00Z","is_active":true},{"kind":"weekly_scoped","scope":{"model":{"display_name":"Opus"}},"percent":10,"resets_at":"2026-05-12T12:00:00Z"}]}`
	caller := &recordingManagementCaller{responses: []*apicall.Response{
		{StatusCode: 200, BodyText: usageBody, Body: json.RawMessage(usageBody)},
		{StatusCode: 200, BodyText: `{}`, Body: json.RawMessage(`{}`)},
	}}
	configs := quota.DefaultProviderConfigs()
	provider := quota.NewClaudeProvider(caller, configs.ClaudeUsage, configs.ClaudeProfile)

	output, err := provider.Check(context.Background(), quota.ProviderInput{Identity: entities.UsageIdentity{Identity: "claude-auth"}})
	if err != nil {
		t.Fatalf("Check returned error: %v", err)
	}
	result := output.Result.(quota.ClaudeResult)
	if result.Usage == nil || len(result.Usage.Limits) != 2 {
		t.Fatalf("expected two parsed scoped limits, got %#v", result.Usage)
	}
	fable := result.Usage.Limits[0]
	if fable.Kind != "weekly_scoped" || fable.ModelName != "Fable 5" || fable.Percent == nil || *fable.Percent != 44.5 || fable.ResetsAt != "2026-05-14T12:00:00Z" || !fable.IsActive {
		t.Fatalf("unexpected fable limit: %#v", fable)
	}
	if opus := result.Usage.Limits[1]; opus.ModelName != "Opus" || opus.IsActive {
		t.Fatalf("unexpected opus limit: %#v", opus)
	}

	rows := quota.NormalizeQuotaRows(output)
	var seen bool
	for _, row := range rows {
		if row.Key != "seven_day_fable" {
			continue
		}
		seen = true
		if row.Label != "7d Fable 5" || row.UsedPercent == nil || *row.UsedPercent != 44.5 {
			t.Fatalf("unexpected fable row: %#v", row)
		}
	}
	if !seen {
		t.Fatalf("expected a fable quota row, got %#v", rows)
	}
}

func TestClaudeProviderKeepsUsageWhenProfileIsUnavailable(t *testing.T) {
	tests := []struct {
		name     string
		response *apicall.Response
	}{
		{name: "non 2xx", response: &apicall.Response{StatusCode: 503, BodyText: `{"message":"unavailable"}`}},
		{name: "parse failure", response: &apicall.Response{StatusCode: 200, BodyText: `not-json`, Body: json.RawMessage(`null`)}},
		{name: "empty body", response: &apicall.Response{StatusCode: 200}},
		{name: "missing response"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			caller := &recordingManagementCaller{responses: []*apicall.Response{
				quotaAPIResponse(200, `{"five_hour":{"utilization":36}}`),
				test.response,
			}}
			configs := quota.DefaultProviderConfigs()
			provider := quota.NewClaudeProvider(caller, configs.ClaudeUsage, configs.ClaudeProfile)

			output, err := provider.Check(context.Background(), quota.ProviderInput{Identity: entities.UsageIdentity{Identity: "claude-auth"}})
			if err != nil {
				t.Fatalf("Check returned error: %v", err)
			}
			result, ok := output.Result.(quota.ClaudeResult)
			if !ok || result.Usage == nil || result.Usage.FiveHour == nil || result.Usage.FiveHour.Utilization != 36 {
				t.Fatalf("expected usage to be preserved, got %#v", output.Result)
			}
			if result.Profile != nil {
				t.Fatalf("expected unavailable profile to be omitted, got %#v", result.Profile)
			}
			if len(caller.requests) != 2 {
				t.Fatalf("expected serial usage and profile requests, got %d", len(caller.requests))
			}
		})
	}
}

func TestClaudeProviderSkipsProfileWhenUsageFails(t *testing.T) {
	tests := []struct {
		name     string
		response *apicall.Response
	}{
		{name: "non 2xx", response: &apicall.Response{StatusCode: 500, BodyText: `{"message":"usage failed"}`}},
		{name: "parse failure", response: &apicall.Response{StatusCode: 200, BodyText: `not-json`, Body: json.RawMessage(`null`)}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			caller := &recordingManagementCaller{responses: []*apicall.Response{
				test.response,
				quotaAPIResponse(200, `{}`),
			}}
			configs := quota.DefaultProviderConfigs()
			provider := quota.NewClaudeProvider(caller, configs.ClaudeUsage, configs.ClaudeProfile)

			_, err := provider.Check(context.Background(), quota.ProviderInput{Identity: entities.UsageIdentity{Identity: "claude-auth"}})
			if err == nil {
				t.Fatal("expected usage failure")
			}
			if len(caller.requests) != 1 || caller.requests[0].URL != configs.ClaudeUsage.URL {
				t.Fatalf("expected only the usage request, got %+v", caller.requests)
			}
		})
	}
}

func TestClaudeProviderKeepsNullSubscriptionFlagsUnknown(t *testing.T) {
	caller := &recordingManagementCaller{responses: []*apicall.Response{
		quotaAPIResponse(200, `{}`),
		quotaAPIResponse(200, `{"account":{"has_claude_max":null,"has_claude_pro":null}}`),
	}}
	configs := quota.DefaultProviderConfigs()
	provider := quota.NewClaudeProvider(caller, configs.ClaudeUsage, configs.ClaudeProfile)

	output, err := provider.Check(context.Background(), quota.ProviderInput{Identity: entities.UsageIdentity{Identity: "claude-auth"}})
	if err != nil {
		t.Fatalf("Check returned error: %v", err)
	}
	result := output.Result.(quota.ClaudeResult)
	if result.Profile == nil || result.Profile.Account == nil {
		t.Fatalf("expected parsed profile account, got %#v", result.Profile)
	}
	if result.Profile.Account.HasClaudeMax != nil || result.Profile.Account.HasClaudePro != nil {
		t.Fatalf("expected null flags to stay unknown, got %#v", result.Profile.Account)
	}
	if subscription := quota.NormalizeSubscription(output); subscription != nil {
		t.Fatalf("expected unknown subscription, got %#v", subscription)
	}
}

type claudeProfileContextCaller struct {
	calls           int
	profileDeadline time.Time
	profileErr      error
}

func (c *claudeProfileContextCaller) CallManagementAPI(ctx context.Context, _ apicall.Request) (*apicall.Response, error) {
	c.calls++
	if c.calls == 1 {
		return quotaAPIResponse(200, `{"five_hour":{"utilization":36}}`), nil
	}
	c.profileDeadline, _ = ctx.Deadline()
	return nil, c.profileErr
}

func TestClaudeProviderBoundsOptionalProfileContext(t *testing.T) {
	tests := []struct {
		name          string
		parentTimeout time.Duration
		maxRemaining  time.Duration
		minRemaining  time.Duration
		profileErr    error
	}{
		{name: "ten second helper timeout", minRemaining: 9 * time.Second, maxRemaining: 11 * time.Second, profileErr: context.DeadlineExceeded},
		{name: "inherits earlier parent deadline", parentTimeout: 250 * time.Millisecond, maxRemaining: 350 * time.Millisecond, profileErr: context.DeadlineExceeded},
		{name: "network error", minRemaining: 9 * time.Second, maxRemaining: 11 * time.Second, profileErr: errors.New("network unavailable")},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctx := context.Background()
			if test.parentTimeout > 0 {
				var cancel context.CancelFunc
				ctx, cancel = context.WithTimeout(ctx, test.parentTimeout)
				defer cancel()
			}
			caller := &claudeProfileContextCaller{profileErr: test.profileErr}
			configs := quota.DefaultProviderConfigs()
			provider := quota.NewClaudeProvider(caller, configs.ClaudeUsage, configs.ClaudeProfile)

			output, err := provider.Check(ctx, quota.ProviderInput{Identity: entities.UsageIdentity{Identity: "claude-auth"}})
			if err != nil {
				t.Fatalf("Check returned error: %v", err)
			}
			result := output.Result.(quota.ClaudeResult)
			if result.Usage == nil || result.Profile != nil {
				t.Fatalf("expected usage without optional profile, got %#v", result)
			}
			if caller.calls != 2 || caller.profileDeadline.IsZero() {
				t.Fatalf("expected profile call with deadline, calls=%d deadline=%v", caller.calls, caller.profileDeadline)
			}
			remaining := time.Until(caller.profileDeadline)
			if remaining < test.minRemaining || remaining > test.maxRemaining {
				t.Fatalf("unexpected profile deadline remaining %s", remaining)
			}
		})
	}
}
