package keypolicy

// Adjustments 按窗口记录一个 key 当前生效的带符号调整额（美元）；
// 调整只影响本周期，窗口翻页或被清除后不再出现在这个 map 里。
type Adjustments map[LimitWindow]float64

// EffectiveLimit 是叠加调整后的限额：Limit 字段是生效值，Base 保留原始限额，
// Adjustment 非 nil 时记录当前生效的那笔调整额，用于展示 "temp +$500" 之类的标记。
type EffectiveLimit struct {
	Limit
	Base       float64
	Adjustment *float64
}

// EffectiveLimits 是一个 key 叠加调整后的全部限额，顺序与输入的 Limits 一致。
type EffectiveLimits []EffectiveLimit

// ApplyAdjustments 把带符号的调整额叠加到每条正常限额上：
// effective = max(base + delta, 0)，下限为 0 但不允许变成负数（负数没有意义）。
// 没有正常限额的窗口即使存在调整也忽略（调整只能收紧/放宽已有的限额）。
// 不修改输入的 limits，调用方的切片可以放心复用。
func ApplyAdjustments(limits Limits, adjustments Adjustments) EffectiveLimits {
	result := make(EffectiveLimits, 0, len(limits))
	for _, limit := range limits {
		effective := EffectiveLimit{Limit: limit, Base: limit.Value}
		if delta, ok := adjustments[limit.Window]; ok {
			value := limit.Value + delta
			if value < 0 {
				value = 0
			}
			effective.Value = value
			effective.Adjustment = &delta
		}
		result = append(result, effective)
	}
	return result
}

// Limits 剥离 Base/Adjustment，只留给 Evaluate 用的生效值。
func (limits EffectiveLimits) Limits() Limits {
	result := make(Limits, len(limits))
	for i, limit := range limits {
		result[i] = limit.Limit
	}
	return result
}

// Tightest 返回按生效限额算出的已用比例最高的一条，排序和空限额规则与 Limits.Tightest 一致。
func (limits EffectiveLimits) Tightest(usage UsageByWindow) *TightestLimit {
	var tightest *TightestLimit
	for _, limit := range limits {
		used := usedValue(limit.Type, usage[limit.Window])
		ratio := limitRatio(used, limit.Value)
		if tightest == nil || ratio > tightest.Ratio {
			tightest = &TightestLimit{Limit: limit.Limit, Used: used, Ratio: ratio, Base: limit.Base, Adjustment: limit.Adjustment}
		}
	}
	return tightest
}

// WindowUsages 按日 → 周 → 月返回每条生效限额的用量与比例，规则与 Limits.WindowUsages 一致。
func (limits EffectiveLimits) WindowUsages(usage UsageByWindow) []TightestLimit {
	result := make([]TightestLimit, 0, len(limits))
	for _, limit := range limits {
		used := usedValue(limit.Type, usage[limit.Window])
		result = append(result, TightestLimit{Limit: limit.Limit, Used: used, Ratio: limitRatio(used, limit.Value), Base: limit.Base, Adjustment: limit.Adjustment})
	}
	sortWindowUsages(result)
	return result
}
