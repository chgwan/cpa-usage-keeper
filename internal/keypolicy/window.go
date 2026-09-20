package keypolicy

import (
	"fmt"
	"time"
)

// Window 是一段本地日历周期，Start 含头、End 不含尾。
type Window struct {
	Kind  LimitWindow
	Start time.Time
	End   time.Time
}

// WindowKey 返回周期的唯一键：日窗口为日期，周窗口为 ISO 周（如 2026-W38），月窗口为年月。
// 三种格式互不重叠，DisabledWindowKey 的翻转判断不会串窗。
func WindowKey(w Window) string {
	switch w.Kind {
	case LimitWindowMonthly:
		return w.Start.Format("2006-01")
	case LimitWindowWeekly:
		year, week := w.Start.ISOWeek()
		return fmt.Sprintf("%04d-W%02d", year, week)
	}
	return w.Start.Format("2006-01-02")
}

// DailyWindow 以项目 TZ（time.Local 已在 config 载入时固化）的当日零点为界。
func DailyWindow(now time.Time) Window {
	start := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	return Window{Kind: LimitWindowDaily, Start: start, End: start.AddDate(0, 0, 1)}
}

// WeeklyWindow 以项目 TZ 的本周一零点为界（ISO 惯例，周日归属前一个周一起算的那周）。
func WeeklyWindow(now time.Time) Window {
	day := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	// time.Weekday 周日为 0；换算成「距周一的天数」。
	offset := (int(day.Weekday()) + 6) % 7
	start := day.AddDate(0, 0, -offset)
	return Window{Kind: LimitWindowWeekly, Start: start, End: start.AddDate(0, 0, 7)}
}

// MonthlyWindow 以项目 TZ 的当月一日零点为界。
func MonthlyWindow(now time.Time) Window {
	start := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, now.Location())
	return Window{Kind: LimitWindowMonthly, Start: start, End: start.AddDate(0, 1, 0)}
}
