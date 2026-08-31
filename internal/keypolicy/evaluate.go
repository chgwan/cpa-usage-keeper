package keypolicy

// Usage 是一个 key 在单个周期窗口内的用量快照。
type Usage struct {
	Requests int64
	Tokens   int64
	CostUSD  float64
}

// UsageByWindow 按窗口缓存一次查询得到的两组用量。
type UsageByWindow map[LimitWindow]Usage

// Breach 描述触发禁用的那条限额与当时的实际用量。
type Breach struct {
	Limit Limit
	Used  float64
}

// usedValue 把指定维度的用量归一成 float64 参与 >= 比较。
func usedValue(limitType LimitType, usage Usage) float64 {
	switch limitType {
	case LimitTypeRequests:
		return float64(usage.Requests)
	case LimitTypeTokens:
		return float64(usage.Tokens)
	case LimitTypeCost:
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
type TightestLimit struct {
	Limit Limit
	Used  float64
	Ratio float64
}

// Tightest 返回已用比例最高的一条限额，无限额时返回 nil。
func (limits Limits) Tightest(usage UsageByWindow) *TightestLimit {
	var tightest *TightestLimit
	for _, limit := range limits {
		used := usedValue(limit.Type, usage[limit.Window])
		ratio := used / limit.Value
		if tightest == nil || ratio > tightest.Ratio {
			tightest = &TightestLimit{Limit: limit, Used: used, Ratio: ratio}
		}
	}
	return tightest
}
