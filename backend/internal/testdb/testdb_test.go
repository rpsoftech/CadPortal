package testdb

import "testing"

func TestNameIsPerPackage(t *testing.T) {
	cases := map[string]string{
		`/src/backend/internal/service`:   "cad_portal_internal_service_test",
		`/src/backend/internal/handlers`:  "cad_portal_internal_handlers_test",
		`E:\code\backend\internal\Backup`: "cad_portal_internal_backup_test",
		`/src/backend/cmd/backup`:         "cad_portal_cmd_backup_test",
	}
	seen := map[string]string{}
	for dir, want := range cases {
		got := Name(dir)
		if got != want {
			t.Errorf("Name(%q) = %q want %q", dir, got, want)
		}
		if prev, dup := seen[got]; dup {
			t.Errorf("%q and %q share database %q", prev, dir, got)
		}
		seen[got] = dir
		if len(got) > 63 || got[len(got)-5:] != "_test" {
			t.Errorf("bad name %q", got)
		}
	}
}
