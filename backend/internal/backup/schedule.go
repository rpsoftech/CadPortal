package backup

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// ParseTimeOfDay reads "HH:MM" (24 hour).
func ParseTimeOfDay(s string) (hour, min int, err error) {
	h, m, ok := strings.Cut(strings.TrimSpace(s), ":")
	hour, err1 := strconv.Atoi(h)
	min, err2 := strconv.Atoi(m)
	if !ok || err1 != nil || err2 != nil || hour < 0 || hour > 23 || min < 0 || min > 59 {
		return 0, 0, fmt.Errorf("BACKUP_AT must look like 02:00 (24 hour), got %q", s)
	}
	return hour, min, nil
}

// NextRun is the first time after now that the clock in loc shows hour:min.
func NextRun(now time.Time, hour, min int, loc *time.Location) time.Time {
	n := now.In(loc)
	t := time.Date(n.Year(), n.Month(), n.Day(), hour, min, 0, 0, loc)
	if !t.After(n) {
		t = time.Date(n.Year(), n.Month(), n.Day()+1, hour, min, 0, 0, loc)
	}
	return t
}
