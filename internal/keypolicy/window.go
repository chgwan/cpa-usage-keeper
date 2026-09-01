package keypolicy

import "time"

// Window 是一段本地日历周期，Start 含头、End 不含尾。
type Window struct {
	Kind  LimitWindow
	Start time.Time
	End   time.Time
}

// WindowKey 返回周期的唯一键：日窗口为日期，月窗口为年月。
func WindowKey(w Window) string {
	if w.Kind == LimitWindowMonthly {
		return w.Start.Format("2006-01")
	}
	return w.Start.Format("2006-01-02")
}

// DailyWindow 以项目 TZ（time.Local 已在 config 载入时固化）的当日零点为界。
func DailyWindow(now time.Time) Window {
	start := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	return Window{Kind: LimitWindowDaily, Start: start, End: start.AddDate(0, 0, 1)}
}

// MonthlyWindow 以项目 TZ 的当月一日零点为界。
func MonthlyWindow(now time.Time) Window {
	start := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, now.Location())
	return Window{Kind: LimitWindowMonthly, Start: start, End: start.AddDate(0, 1, 0)}
}
