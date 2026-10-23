// Package backup holds what the backup service does: dump the database, copy new files to a second
// bucket, and thin out old dumps. It is separate from the API so the API keeps no shell or pg_dump.
package backup

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"
)

// dumpPrefix is where database dumps live in the backup bucket; copied files live under filesPrefix.
const (
	dumpPrefix  = "db/"
	filesPrefix = "files/"
)

// Policy says how many dumps to keep. Of each period the newest dump is the one kept. Yearly dumps are
// never deleted. On top of that, every dump younger than KeepRecent is kept, so a schedule with several
// runs a day keeps its intraday dumps for a while instead of thinning them to one a day at once.
type Policy struct {
	Daily, Weekly, Monthly int
	KeepRecent             time.Duration // 0 = off
}

// DefaultPolicy is 7 daily, 4 weekly and 12 monthly dumps, plus one for every year, forever, and every
// dump of the last 48 hours.
var DefaultPolicy = Policy{Daily: 7, Weekly: 4, Monthly: 12, KeepRecent: 48 * time.Hour}

// Dump is a database dump found in the backup bucket.
type Dump struct {
	Key  string
	Time time.Time // when it was taken (UTC), read from its name
}

var dumpName = regexp.MustCompile(`^db/cad_portal-(\d{8}-\d{6})\.dump(\.age)?$`)

// DumpKey is the object name for a dump taken at t. Names sort in time order.
func DumpKey(t time.Time, encrypted bool) string {
	k := dumpPrefix + "cad_portal-" + t.UTC().Format("20060102-150405") + ".dump"
	if encrypted {
		k += ".age"
	}
	return k
}

// ParseDump reads the time from a dump's key; ok is false for anything that is not a dump this
// service wrote, so a stray object is never mistaken for one (and never pruned).
func ParseDump(key string) (Dump, bool) {
	m := dumpName.FindStringSubmatch(key)
	if m == nil {
		return Dump{}, false
	}
	t, err := time.ParseInLocation("20060102-150405", m[1], time.UTC)
	if err != nil {
		return Dump{}, false
	}
	return Dump{Key: key, Time: t}, true
}

// Prune decides which of dumps to delete. A dump survives if it is the newest of one of the last
// Daily days, Weekly ISO weeks or Monthly months that have a dump, or the newest of its year. The newest
// dump of all is always kept, whatever the policy says, and so is every dump taken within KeepRecent of now.
func (p Policy) Prune(now time.Time, dumps []Dump) (keep, drop []Dump) {
	sorted := append([]Dump(nil), dumps...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Time.After(sorted[j].Time) })

	kept := map[string]bool{}
	pick := func(limit int, bucket func(time.Time) string) {
		seen := map[string]bool{}
		for _, d := range sorted {
			b := bucket(d.Time)
			if seen[b] {
				continue
			}
			if limit >= 0 && len(seen) >= limit {
				return
			}
			seen[b] = true
			kept[d.Key] = true
		}
	}
	pick(p.Daily, func(t time.Time) string { return t.Format("2006-01-02") })
	pick(p.Weekly, func(t time.Time) string { y, w := t.ISOWeek(); return fmt.Sprintf("%d-W%02d", y, w) })
	pick(p.Monthly, func(t time.Time) string { return t.Format("2006-01") })
	pick(-1, func(t time.Time) string { return t.Format("2006") })
	if len(sorted) > 0 {
		kept[sorted[0].Key] = true
	}
	if p.KeepRecent > 0 {
		for _, d := range sorted {
			if now.Sub(d.Time) < p.KeepRecent {
				kept[d.Key] = true
			}
		}
	}
	for _, d := range sorted {
		if kept[d.Key] {
			keep = append(keep, d)
		} else {
			drop = append(drop, d)
		}
	}
	return keep, drop
}

// IsEncrypted reports whether key names an age-encrypted dump.
func IsEncrypted(key string) bool { return strings.HasSuffix(key, ".age") }
