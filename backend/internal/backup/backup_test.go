package backup

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"testing"
	"time"

	"filippo.io/age"
)

func at(s string) time.Time {
	t, err := time.Parse("2006-01-02 15:04", s)
	if err != nil {
		panic(err)
	}
	return t
}

func dumpsEvery(day time.Time, n int) []Dump {
	var out []Dump
	for i := 0; i < n; i++ {
		t := day.AddDate(0, 0, -i).Add(2 * time.Hour)
		out = append(out, Dump{Key: DumpKey(t, false), Time: t})
	}
	return out
}

func keys(ds []Dump) []string {
	var out []string
	for _, d := range ds {
		out = append(out, strings.TrimSuffix(strings.TrimPrefix(d.Key, "db/cad_portal-"), ".dump"))
	}
	sort.Strings(out)
	return out
}

func TestDumpKeyRoundTrip(t *testing.T) {
	for _, enc := range []bool{false, true} {
		k := DumpKey(at("2026-10-22 02:00"), enc)
		d, ok := ParseDump(k)
		if !ok || !d.Time.Equal(at("2026-10-22 02:00")) || IsEncrypted(k) != enc {
			t.Errorf("%s: parsed %v %v", k, d, ok)
		}
	}
	for _, bad := range []string{"db/", "db/notes.txt", "files/db/cad_portal-20261022-020000.dump", "db/cad_portal-2026.dump"} {
		if _, ok := ParseDump(bad); ok {
			t.Errorf("%q must not parse as a dump", bad)
		}
	}
}

func TestPruneKeepsDailyWeeklyMonthlyAndEveryYear(t *testing.T) {
	// One dump a night for 3 years: 1095 dumps.
	dumps := dumpsEvery(at("2028-12-31 00:00"), 1095)
	keep, drop := DefaultPolicy.Prune(at("2031-01-01 00:00"), dumps)
	if len(keep)+len(drop) != len(dumps) {
		t.Fatalf("keep %d + drop %d != %d", len(keep), len(drop), len(dumps))
	}
	got := map[string]bool{}
	for _, k := range keys(keep) {
		got[k[:8]] = true // the date
	}
	// The last 7 nights.
	for i := 0; i < 7; i++ {
		d := at("2028-12-31 00:00").AddDate(0, 0, -i).Format("20060102")
		if !got[d] {
			t.Errorf("daily dump %s was dropped", d)
		}
	}
	// A year-end dump for every year in the data stays: the newest of 2028, 2027, 2026.
	for _, y := range []string{"20281231", "20271231", "20261231"} {
		if !got[y] {
			t.Errorf("yearly dump %s was dropped", y)
		}
	}
	// Roughly: 7 + 4 + 12 + 3, minus overlaps. Far fewer than 1095, and never fewer than the yearly floor.
	if n := len(keep); n < 15 || n > 26 {
		t.Errorf("kept %d dumps, expected between 15 and 26", n)
	}
	// A mid-2027 night is not one of the kept ones.
	if got["20270615"] {
		t.Error("an ordinary night from 18 months ago must be pruned")
	}
}

func TestPruneTable(t *testing.T) {
	cases := []struct {
		name   string
		dumps  []string // yyyy-mm-dd hh:mm
		policy Policy
		want   []string // kept, as yyyymmdd-hhmmss
	}{
		{"nothing to prune", []string{"2026-10-20 02:00", "2026-10-21 02:00"}, DefaultPolicy,
			[]string{"20261020-020000", "20261021-020000"}},
		{"only the newest of a day is kept", []string{"2026-10-21 01:00", "2026-10-21 02:00", "2026-10-21 23:00"}, Policy{Daily: 1},
			[]string{"20261021-230000"}},
		{"the newest dump survives an empty policy", []string{"2026-10-20 02:00", "2026-10-21 02:00"}, Policy{},
			[]string{"20261021-020000"}},
		{"a gap in days does not shorten the daily window", []string{"2026-10-01 02:00", "2026-10-10 02:00", "2026-10-20 02:00"}, Policy{Daily: 2},
			[]string{"20261010-020000", "20261020-020000"}},
		{"yearly dumps are never dropped", []string{"2024-06-01 02:00", "2025-06-01 02:00", "2026-10-20 02:00"}, Policy{},
			[]string{"20240601-020000", "20250601-020000", "20261020-020000"}},
		{"weekly keeps one per ISO week", []string{"2026-10-05 02:00", "2026-10-07 02:00", "2026-10-14 02:00", "2026-10-20 02:00"}, Policy{Weekly: 2},
			[]string{"20261014-020000", "20261020-020000"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var ds []Dump
			for _, s := range c.dumps {
				ds = append(ds, Dump{Key: DumpKey(at(s), false), Time: at(s)})
			}
			keep, _ := c.policy.Prune(at("2031-01-01 00:00"), ds)
			got := keys(keep)
			// A year's newest dump is kept as well, so compare against the expectation plus yearly floors.
			if fmt.Sprint(got) != fmt.Sprint(c.want) {
				t.Errorf("kept %v, want %v", got, c.want)
			}
		})
	}
}

func TestNextRun(t *testing.T) {
	ist := time.FixedZone("IST", 5*3600+1800)
	one := []TimeOfDay{{2, 0}}
	four := []TimeOfDay{{2, 0}, {8, 0}, {14, 0}, {20, 0}}
	cases := []struct {
		name  string
		times []TimeOfDay
		now   string
		want  string
	}{
		{"before the night run", one, "2026-10-22 01:00", "2026-10-22 02:00"},
		{"exactly at the time: the next night", one, "2026-10-22 02:00", "2026-10-23 02:00"},
		{"late evening", one, "2026-10-22 23:59", "2026-10-23 02:00"},
		{"four a day, before the first", four, "2026-10-22 01:00", "2026-10-22 02:00"},
		{"four a day, between", four, "2026-10-22 09:30", "2026-10-22 14:00"},
		{"four a day, exactly at one", four, "2026-10-22 14:00", "2026-10-22 20:00"},
		{"four a day, after the last", four, "2026-10-22 20:01", "2026-10-23 02:00"},
		{"month end rolls over", four, "2026-10-31 21:00", "2026-11-01 02:00"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := NextRun(at(c.now).In(time.UTC), c.times, time.UTC)
			if !got.Equal(at(c.want)) {
				t.Errorf("got %v want %s", got, c.want)
			}
		})
	}
	// 02:00 in India is 20:30 UTC the evening before.
	got := NextRun(at("2026-10-22 12:00"), one, ist)
	if want := at("2026-10-22 20:30"); !got.Equal(want) {
		t.Errorf("IST: got %v want %v", got, want)
	}
	for _, bad := range []string{"", "2", "24:00", "02:60", "ab:cd"} {
		if _, _, err := ParseTimeOfDay(bad); err == nil {
			t.Errorf("%q should be refused", bad)
		}
	}
}

func TestParseTimesOfDay(t *testing.T) {
	ok := []struct{ in, want string }{
		{"02:00", "[02:00]"},
		{"02:00,08:00,14:00,20:00", "[02:00 08:00 14:00 20:00]"},
		{"20:00, 02:00 ,14:00,08:00", "[02:00 08:00 14:00 20:00]"}, // spaces and order do not matter
		{"02:00,02:00,2:00", "[02:00]"},                            // duplicates, incl. 2:00 vs 02:00
		{"02:00,", "[02:00]"},
		{"23:59,00:00", "[00:00 23:59]"},
	}
	for _, c := range ok {
		got, err := ParseTimesOfDay(c.in)
		if err != nil || fmt.Sprint(got) != c.want {
			t.Errorf("%q: got %v, %v want %s", c.in, got, err, c.want)
		}
	}
	for _, bad := range []string{"", " ", ",", "02:00,24:00", "02:00;08:00", "02:00,abc", "02:00,08:61"} {
		if got, err := ParseTimesOfDay(bad); err == nil {
			t.Errorf("%q should be refused, got %v", bad, got)
		}
	}
}

func TestMaxGapAndCatchUp(t *testing.T) {
	cases := []struct {
		in      string
		gap, up time.Duration
	}{
		{"02:00", 24 * time.Hour, 26 * time.Hour},
		{"02:00,08:00,14:00,20:00", 6 * time.Hour, 8 * time.Hour},
		{"00:00,12:00", 12 * time.Hour, 14 * time.Hour},
		{"06:00,07:00", 23 * time.Hour, 25 * time.Hour},
	}
	for _, c := range cases {
		ts, err := ParseTimesOfDay(c.in)
		if err != nil {
			t.Fatal(err)
		}
		if g := MaxGap(ts); g != c.gap {
			t.Errorf("%s: gap %v want %v", c.in, g, c.gap)
		}
		if u := CatchUpAfter(ts); u != c.up {
			t.Errorf("%s: catch-up %v want %v", c.in, u, c.up)
		}
	}
}

func TestPruneKeepRecent(t *testing.T) {
	// Four dumps a day for four days, newest 2026-10-24 20:00.
	var times []string
	for d := 21; d <= 24; d++ {
		for _, h := range []string{"02", "08", "14", "20"} {
			times = append(times, fmt.Sprintf("2026-10-%d %s:00", d, h))
		}
	}
	mk := func() (ds []Dump) {
		for _, s := range times {
			ds = append(ds, Dump{Key: DumpKey(at(s), false), Time: at(s)})
		}
		return ds
	}
	now := at("2026-10-24 20:30")
	dayOnly := Policy{Daily: 2}
	if keep, _ := dayOnly.Prune(now, mk()); len(keep) != 2 { // the 24th's and 23rd's last dumps (a 2 day window)
		t.Errorf("without KeepRecent kept %v", keys(keep))
	}
	p := Policy{Daily: 2, KeepRecent: 48 * time.Hour}
	keep, drop := p.Prune(now, mk())
	got := map[string]bool{}
	for _, k := range keys(keep) {
		got[k] = true
	}
	// everything after 2026-10-22 20:30 is within 48 hours: 23rd (4) + 24th (4) = 8, and the 22nd 20:00 is just outside
	// but it is not the newest of a kept day, so it goes.
	if len(keep) != 8 {
		t.Errorf("kept %d (%v) want 8", len(keep), keys(keep))
	}
	for _, k := range []string{"20261023-020000", "20261023-080000", "20261024-140000", "20261024-200000"} {
		if !got[k] {
			t.Errorf("recent dump %s was dropped", k)
		}
	}
	if got["20261022-200000"] || got["20261021-200000"] {
		t.Error("an older intraday dump that is not a day's newest must be pruned")
	}
	if len(keep)+len(drop) != len(times) {
		t.Error("keep + drop must cover all dumps")
	}
	// A dump exactly KeepRecent old is no longer "recent"; one a minute younger is.
	edge := []Dump{
		{Key: DumpKey(at("2026-10-22 20:30"), false), Time: at("2026-10-22 20:30")},
		{Key: DumpKey(at("2026-10-22 20:31"), false), Time: at("2026-10-22 20:31")},
		{Key: DumpKey(at("2026-10-24 20:30"), false), Time: at("2026-10-24 20:30")},
	}
	keep, _ = Policy{KeepRecent: 48 * time.Hour}.Prune(now, edge)
	if fmt.Sprint(keys(keep)) != "[20261022-203100 20261024-203000]" {
		t.Errorf("edge: kept %v", keys(keep))
	}
}

type fakeRec struct {
	started int
	results []Result
}

func (f *fakeRec) Start(context.Context) (int64, error) { f.started++; return int64(f.started), nil }
func (f *fakeRec) Finish(_ context.Context, _ int64, r Result) error {
	f.results = append(f.results, r)
	return nil
}

func newRunner(src, dest *Memory, dump string) (*Runner, *fakeRec) {
	rec := &fakeRec{}
	r := &Runner{
		Rec: rec, Dest: dest, Policy: DefaultPolicy, TmpDir: os.TempDir(),
		Dump: func(_ context.Context, w io.Writer) error {
			_, err := io.WriteString(w, dump)
			return err
		},
		Verify: func(context.Context, string) error { return nil },
		Now:    func() time.Time { return at("2026-10-22 02:00") },
	}
	if src != nil {
		r.Source = src
	}
	return r, rec
}

func TestRunCopiesFilesIncrementallyKeepingTheirEncoding(t *testing.T) {
	src, dest := NewMemory(), NewMemory()
	put := func(m *Memory, k, body string, meta Meta) {
		_ = m.Put(context.Background(), k, strings.NewReader(body), meta)
	}
	put(src, "cad/LR0001/1/a.3dm.gz", "gzbytes", Meta{ContentType: "application/octet-stream", ContentEncoding: "gzip"})
	put(src, "media/LR0001/p.jpg", "jpeg", Meta{ContentType: "image/jpeg"})
	put(src, "r2-test/x", "test data", Meta{})

	r, rec := newRunner(src, dest, "DUMPDATA")
	res := r.Run(context.Background())
	if !res.OK || res.FilesCopied != 2 || res.FilesTotal != 2 || res.DBBytes != 8 {
		t.Fatalf("first run: %+v", res)
	}
	if string(dest.Objects["files/cad/LR0001/1/a.3dm.gz"]) != "gzbytes" || dest.Metas["files/cad/LR0001/1/a.3dm.gz"].ContentEncoding != "gzip" {
		t.Errorf("a copied CAD file must keep its bytes and Content-Encoding: %+v", dest.Metas)
	}
	if dest.Metas["files/media/LR0001/p.jpg"].ContentType != "image/jpeg" {
		t.Error("a copied photo must keep its type")
	}
	if _, ok := dest.Objects["files/r2-test/x"]; ok {
		t.Error("integration-test objects are not backed up")
	}
	if string(dest.Objects[DumpKey(at("2026-10-22 02:00"), false)]) != "DUMPDATA" {
		t.Errorf("the dump was not stored: %v", keysOf(dest))
	}

	// Second run: only what is new is copied.
	put(src, "media/LR0001/q.jpg", "jpeg2", Meta{ContentType: "image/jpeg"})
	res = r.Run(context.Background())
	if !res.OK || res.FilesCopied != 1 || res.FilesTotal != 3 {
		t.Fatalf("second run: %+v", res)
	}
	// A file removed from the vault stays in the backup.
	_ = src.Delete(context.Background(), "media/LR0001/p.jpg")
	res = r.Run(context.Background())
	if !res.OK || res.FilesCopied != 0 {
		t.Fatalf("third run: %+v", res)
	}
	if _, ok := dest.Objects["files/media/LR0001/p.jpg"]; !ok {
		t.Error("a file removed in the portal must stay in the backup")
	}
	if len(rec.results) != 3 {
		t.Errorf("every run is recorded, got %d", len(rec.results))
	}
}

func TestAFailedDumpIsNeverStoredButFilesAreStillCopied(t *testing.T) {
	src, dest := NewMemory(), NewMemory()
	_ = src.Put(context.Background(), "cad/a.gz", strings.NewReader("x"), Meta{})
	r, _ := newRunner(src, dest, "x")
	r.Dump = func(context.Context, io.Writer) error { return errors.New("connection refused") }
	res := r.Run(context.Background())
	if res.OK || !strings.Contains(res.Err, "connection refused") {
		t.Fatalf("want a recorded failure, got %+v", res)
	}
	if res.FilesCopied != 1 {
		t.Errorf("files must still be copied when the dump fails: %+v", res)
	}
	for k := range dest.Objects {
		if strings.HasPrefix(k, "db/") {
			t.Errorf("nothing should be stored for a failed dump, found %s", k)
		}
	}

	// An unreadable dump is refused the same way.
	r, _ = newRunner(nil, dest, "garbage")
	r.Verify = func(context.Context, string) error { return errors.New("not an archive") }
	if res := r.Run(context.Background()); res.OK || !strings.Contains(res.Err, "unreadable") || len(dest.Objects) != 1 {
		t.Errorf("unreadable dump: %+v", res)
	}
	// And an empty one.
	r, _ = newRunner(nil, dest, "")
	if res := r.Run(context.Background()); res.OK || !strings.Contains(res.Err, "empty") {
		t.Errorf("empty dump: %+v", res)
	}
}

func TestAFileThatFailsToCopyIsReportedAndRetriedNextRun(t *testing.T) {
	src, dest := NewMemory(), NewMemory()
	_ = src.Put(context.Background(), "cad/ok.gz", strings.NewReader("1"), Meta{})
	_ = src.Put(context.Background(), "cad/bad.gz", strings.NewReader("2"), Meta{})
	dest.PutErr["files/cad/bad.gz"] = errors.New("boom")
	r, _ := newRunner(src, dest, "x")
	res := r.Run(context.Background())
	if res.OK || !strings.Contains(res.Err, "cad/bad.gz") || res.FilesCopied != 1 {
		t.Fatalf("got %+v", res)
	}
	delete(dest.PutErr, "files/cad/bad.gz")
	if res := r.Run(context.Background()); !res.OK || res.FilesCopied != 1 {
		t.Fatalf("retry: %+v", res)
	}
}

func TestRunWithoutAFileStoreBacksUpTheDatabaseOnly(t *testing.T) {
	dest := NewMemory()
	r, _ := newRunner(nil, dest, "x")
	res := r.Run(context.Background())
	if !res.OK || !res.FilesSkipped {
		t.Fatalf("got %+v", res)
	}
}

func TestRunPrunesOldDumpsButNotStrangers(t *testing.T) {
	dest := NewMemory()
	for _, d := range dumpsEvery(at("2026-09-30 00:00"), 40) {
		_ = dest.Put(context.Background(), d.Key, strings.NewReader("old"), Meta{})
	}
	_ = dest.Put(context.Background(), "db/README.txt", strings.NewReader("hi"), Meta{})
	r, _ := newRunner(nil, dest, "new")
	res := r.Run(context.Background())
	if !res.OK {
		t.Fatalf("%+v", res)
	}
	if _, ok := dest.Objects["db/README.txt"]; !ok {
		t.Error("an object that is not a dump must never be deleted")
	}
	if _, ok := dest.Objects[res.DBKey]; !ok {
		t.Error("the new dump must exist")
	}
	n := 0
	for k := range dest.Objects {
		if _, ok := ParseDump(k); ok {
			n++
		}
	}
	if n >= 40 {
		t.Errorf("old dumps were not pruned: %d left", n)
	}
}

func TestEncryptedDumpsOpenOnlyWithTheSecretKey(t *testing.T) {
	id, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	other, _ := age.GenerateX25519Identity()
	dest := NewMemory()
	r, _ := newRunner(nil, dest, strings.Repeat("secret rows ", 1000))
	r.Recipient = id.Recipient()
	res := r.Run(context.Background())
	if !res.OK || !strings.HasSuffix(res.DBKey, ".dump.age") {
		t.Fatalf("%+v", res)
	}
	stored := dest.Objects[res.DBKey]
	if bytes.Contains(stored, []byte("secret rows")) {
		t.Fatal("the dump was stored in the clear")
	}
	plain, err := Decrypt(bytes.NewReader(stored), id.String())
	if err != nil {
		t.Fatal(err)
	}
	got, _ := io.ReadAll(plain)
	if string(got) != strings.Repeat("secret rows ", 1000) {
		t.Error("decrypted dump differs from the original")
	}
	if _, err := Decrypt(bytes.NewReader(stored), other.String()); err == nil {
		t.Error("a different key must not open the dump")
	}
	if _, err := Decrypt(bytes.NewReader(stored), "not a key"); err == nil {
		t.Error("garbage must be refused as a key")
	}
}

func TestRestoreFilesPutsBackOnlyWhatIsMissing(t *testing.T) {
	ctx := context.Background()
	bk, vault := NewMemory(), NewMemory()
	_ = bk.Put(ctx, "files/cad/a.gz", strings.NewReader("A"), Meta{ContentEncoding: "gzip"})
	_ = bk.Put(ctx, "files/cad/b.gz", strings.NewReader("B"), Meta{})
	_ = bk.Put(ctx, "db/cad_portal-20261022-020000.dump", strings.NewReader("D"), Meta{})
	_ = vault.Put(ctx, "cad/b.gz", strings.NewReader("newer B"), Meta{})
	n, err := RestoreFiles(ctx, bk, vault, func(string, ...any) {})
	if err != nil || n != 1 {
		t.Fatalf("restored %d, %v", n, err)
	}
	if string(vault.Objects["cad/a.gz"]) != "A" || vault.Metas["cad/a.gz"].ContentEncoding != "gzip" {
		t.Error("the missing file was not restored with its encoding")
	}
	if string(vault.Objects["cad/b.gz"]) != "newer B" {
		t.Error("an existing file must never be overwritten")
	}
	if _, ok := vault.Objects["db/cad_portal-20261022-020000.dump"]; ok {
		t.Error("dumps are not files")
	}
}

func TestRedactHidesThePassword(t *testing.T) {
	got := redact("connection to postgres://cad:s3cret@db:5432/x failed: password s3cret", "postgres://cad:s3cret@db:5432/x")
	if strings.Contains(got, "s3cret") {
		t.Errorf("password leaked: %s", got)
	}
}

func keysOf(m *Memory) []string {
	var out []string
	for k := range m.Objects {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func TestAfterRunSeesTheOutcomeOfEveryRun(t *testing.T) {
	dest := NewMemory()
	r, _ := newRunner(nil, dest, "x")
	var seen []bool
	r.AfterRun = func(_ context.Context, res Result) { seen = append(seen, res.OK) }
	r.Run(context.Background())
	r.Dump = func(context.Context, io.Writer) error { return errors.New("down") }
	r.Run(context.Background())
	if len(seen) != 2 || !seen[0] || seen[1] {
		t.Errorf("AfterRun outcomes = %v, want [true false]", seen)
	}
}

func TestRetryDelayGrowsThenSettlesAtThirtyMinutes(t *testing.T) {
	want := []time.Duration{time.Minute, 2 * time.Minute, 4 * time.Minute, 8 * time.Minute, 16 * time.Minute, 30 * time.Minute, 30 * time.Minute}
	for i, w := range want {
		if got := RetryDelay(i + 1); got != w {
			t.Errorf("after failure %d: %s, want %s", i+1, got, w)
		}
	}
	if RetryDelay(0) != time.Minute || RetryDelay(-3) != time.Minute {
		t.Error("a count below 1 is treated as the first failure")
	}
}
