package backup

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
)

// TimeOfDay is a wall-clock time of day, set with BACKUP_AT.
type TimeOfDay struct{ Hour, Min int }

func (t TimeOfDay) String() string { return fmt.Sprintf("%02d:%02d", t.Hour, t.Min) }

// ParseTimeOfDay reads "HH:MM" (24 hour).
func ParseTimeOfDay(s string) (hour, min int, err error) {
	h, m, ok := strings.Cut(strings.TrimSpace(s), ":")
	hour, err1 := strconv.Atoi(h)
	min, err2 := strconv.Atoi(m)
	if !ok || err1 != nil || err2 != nil || hour < 0 || hour > 23 || min < 0 || min > 59 {
		return 0, 0, fmt.Errorf("BACKUP_AT must look like 02:00 or 02:00,14:00 (24 hour), got %q", s)
	}
	return hour, min, nil
}

// ParseTimesOfDay reads a comma-separated list such as "02:00,08:00,14:00,20:00". Each entry is
// validated; duplicates are dropped and the result is sorted.
func ParseTimesOfDay(s string) ([]TimeOfDay, error) {
	seen := map[TimeOfDay]bool{}
	var out []TimeOfDay
	for _, part := range strings.Split(s, ",") {
		if strings.TrimSpace(part) == "" {
			continue // a trailing or doubled comma is harmless
		}
		h, m, err := ParseTimeOfDay(part)
		if err != nil {
			return nil, err
		}
		t := TimeOfDay{h, m}
		if !seen[t] {
			seen[t] = true
			out = append(out, t)
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("BACKUP_AT must look like 02:00 or 02:00,14:00 (24 hour), got %q", s)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Hour != out[j].Hour {
			return out[i].Hour < out[j].Hour
		}
		return out[i].Min < out[j].Min
	})
	return out, nil
}

// NextRun is the first time after now that the clock in loc shows one of times.
func NextRun(now time.Time, times []TimeOfDay, loc *time.Location) time.Time {
	n := now.In(loc)
	var best time.Time
	for _, day := range []int{0, 1} { // today and tomorrow always hold one for a non-empty list
		for _, t := range times {
			c := time.Date(n.Year(), n.Month(), n.Day()+day, t.Hour, t.Min, 0, 0, loc)
			if c.After(n) && (best.IsZero() || c.Before(best)) {
				best = c
			}
		}
	}
	return best
}

// MaxGap is the longest wait between two consecutive runs of the schedule (24 hours for one run a
// day), counting the wrap from the last time of one day to the first of the next.
func MaxGap(times []TimeOfDay) time.Duration {
	if len(times) == 0 {
		return 0
	}
	minutes := func(t TimeOfDay) int { return t.Hour*60 + t.Min }
	gap := minutes(times[0]) + 24*60 - minutes(times[len(times)-1])
	for i := 1; i < len(times); i++ {
		if g := minutes(times[i]) - minutes(times[i-1]); g > gap {
			gap = g
		}
	}
	return time.Duration(gap) * time.Minute
}

// CatchUpAfter is how old the last good backup may be at start-up before one is taken at once: the
// longest gap in the schedule plus two hours of slack (26 hours for the classic single nightly run).
func CatchUpAfter(times []TimeOfDay) time.Duration { return MaxGap(times) + 2*time.Hour }
