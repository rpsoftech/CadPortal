package storage

import (
	"mime"
	"strings"
	"testing"
)

func TestDispositionRoundTripsAnyFilename(t *testing.T) {
	for _, name := range []string{
		"ring.3dm",
		"ring v2 final.3dm",
		"bague été — சேரி.stl",
		`quote"inside.3dm`,
		`back\slash.3dm`,
		"semi;colon,comma.3dm",
	} {
		d := Disposition(name)
		typ, params, err := mime.ParseMediaType(d)
		if err != nil || typ != "attachment" || params["filename"] != name {
			t.Errorf("%q -> %q -> %q %v (want the same name back)", name, d, params["filename"], err)
		}
	}
}

func TestDispositionCannotInjectHeaders(t *testing.T) {
	d := Disposition("a.3dm\r\nSet-Cookie: x=1")
	if strings.ContainsAny(d, "\r\n") {
		t.Errorf("control characters survived: %q", d)
	}
	if got := Disposition(""); !strings.HasPrefix(got, "attachment") {
		t.Errorf("empty name: %q", got)
	}
}
