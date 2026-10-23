package keypolicy

import "sort"

// Usage 是一个 key 在单个周期窗口内的用量快照；费用是唯一的限额维度。
type Usage struct {
	CostUSD float64
}

// UsageByWindow 按窗口缓存一次查询得到的三组用量。
type UsageByWindow map[LimitWindow]Usage

// Breach 描述触发禁用的那条限额与当时的实际用量。
type Breach struct {
	Limit Limit
	Used  float64
}

// usedValue 把指定维度的用量归一成 float64 参与 >= 比较。
func usedValue(limitType LimitType, usage Usage) float64 {
	if limitType == LimitTypeCost {
		return usage.CostUSD
	}
	return 0
}

// Evaluate 按声明顺序检查每条限额，used >= value 视为超限，返回首个命中。
func Evaluate(limits Limits, usage UsageByWindow) *Breach {
	for _, limit := range limits {
		used := usedValue(limit.Type, usage[limit.Window])
		if used >= limit.Value {
			return &Breach{Limit: limit, Used: used}
		}
	}
	return nil
}

// TightestLimit 是给 UI 进度条用的最紧张限额。
// Base/Adjustment 记录生效值背后的原始限额与调整额，便于 UI 展示 "temp +$500" 之类的标记；
// 走 Limits.Tightest 这条老路径时 Base == Value 且 Adjustment 为 nil。
type TightestLimit struct {
	Limit      Limit
	Used       float64
	Ratio      float64
	Base       float64
	Adjustment *float64
}

// limitRatio 统一 used/value 的比例口径：value<=0（含被调整额归零的限额）视为已用满，
// 避免除零产生 Inf/NaN，同时保证 0 限额在 UI 上也能显示成 100%。
func limitRatio(used, value float64) float64 {
	if value <= 0 {
		return 1
	}
	return used / value
}

// Tightest 返回已用比例最高的一条限额，无限额时返回 nil。
// 委托给 ApplyAdjustments(limits, nil)，让生效值计算只有一处实现。
func (limits Limits) Tightest(usage UsageByWindow) *TightestLimit {
	return ApplyAdjustments(limits, nil).Tightest(usage)
}

// limitWindowOrder 固定逐窗口展示顺序：日 → 周 → 月。
var limitWindowOrder = map[LimitWindow]int{LimitWindowDaily: 0, LimitWindowWeekly: 1, LimitWindowMonthly: 2}

// sortWindowUsages 按日 → 周 → 月稳定排序，Tightest/WindowUsages 的两个版本共用。
func sortWindowUsages(result []TightestLimit) {
	sort.SliceStable(result, func(left, right int) bool {
		return limitWindowOrder[result[left].Limit.Window] < limitWindowOrder[result[right].Limit.Window]
	})
}

// WindowUsages 按日 → 周 → 月返回每条已配置限额的用量与比例；未配置的窗口不出现。
// 委托给 ApplyAdjustments(limits, nil)，让生效值计算只有一处实现。
func (limits Limits) WindowUsages(usage UsageByWindow) []TightestLimit {
	return ApplyAdjustments(limits, nil).WindowUsages(usage)
}
