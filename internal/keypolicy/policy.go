// Package keypolicy 承载 API Key 用量限额的领域类型、窗口计算和执行判定。
package keypolicy

import (
	"encoding/json"
	"fmt"
)

// LimitType 限定限额统计的维度，当前支持次数、tokens 和费用。
type LimitType string

const (
	LimitTypeRequests LimitType = "requests"
	LimitTypeTokens   LimitType = "tokens"
	LimitTypeCost     LimitType = "cost"
)

// LimitWindow 限定限额统计的周期，跟随项目 TZ 的本地日历。
type LimitWindow string

const (
	LimitWindowDaily   LimitWindow = "daily"
	LimitWindowMonthly LimitWindow = "monthly"
)

// Limit 是一条限额配置：某维度在某周期内允许的最大用量。
type Limit struct {
	Type   LimitType   `json:"type"`
	Window LimitWindow `json:"window"`
	Value  float64     `json:"value"`
}

// Limits 是一个 key 的全部限额，任一条超限即触发禁用。
type Limits []Limit

// ParseLimits 从策略表的 JSON 文本还原限额列表。
func ParseLimits(raw string) (Limits, error) {
	if raw == "" {
		return Limits{}, nil
	}
	var limits Limits
	if err := json.Unmarshal([]byte(raw), &limits); err != nil {
		return nil, fmt.Errorf("parse api key limits: %w", err)
	}
	return limits, nil
}

// Validate 拒绝未知维度、未知周期、非正数和重复的 (维度, 周期) 组合。
func (limits Limits) Validate() error {
	seen := make(map[Limit]struct{}, len(limits))
	for _, limit := range limits {
		switch limit.Type {
		case LimitTypeRequests, LimitTypeTokens, LimitTypeCost:
		default:
			return fmt.Errorf("unknown limit type %q", limit.Type)
		}
		switch limit.Window {
		case LimitWindowDaily, LimitWindowMonthly:
		default:
			return fmt.Errorf("unknown limit window %q", limit.Window)
		}
		if limit.Value <= 0 {
			return fmt.Errorf("limit value must be positive for %s/%s", limit.Type, limit.Window)
		}
		key := Limit{Type: limit.Type, Window: limit.Window}
		if _, ok := seen[key]; ok {
			return fmt.Errorf("duplicate limit for %s/%s", limit.Type, limit.Window)
		}
		seen[key] = struct{}{}
	}
	return nil
}

// EnforcementState 是策略行的执行状态机取值。
type EnforcementState string

const (
	StateActive          EnforcementState = "active"
	StateDisabledByQuota EnforcementState = "disabled_by_quota"
	StateDisabledManual  EnforcementState = "disabled_manual"
)
