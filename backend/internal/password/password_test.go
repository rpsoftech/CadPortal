package password

import (
	"errors"
	"strings"
	"testing"
)

func TestHashAndVerify(t *testing.T) {
	const pw = "correct horse battery staple"
	h, err := Hash(pw)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(h, "$argon2id$v=19$m=65536,t=3,p=2$") {
		t.Fatalf("hash = %s", h)
	}
	if !Verify(pw, h) {
		t.Error("correct password rejected")
	}
	for _, bad := range []string{"", "wrong", pw + " ", strings.ToUpper(pw)} {
		if Verify(bad, h) {
			t.Errorf("accepted %q", bad)
		}
	}
	// Fresh salt every time.
	h2, _ := Hash(pw)
	if h == h2 {
		t.Error("two hashes of the same password are identical")
	}
	if !Verify(pw, h2) {
		t.Error("second hash does not verify")
	}
}

func TestVerifyRejectsMalformedHashes(t *testing.T) {
	good, _ := Hash("a-reasonable-password")
	parts := strings.Split(good, "$")
	cases := map[string]string{
		"empty":           "",
		"plain text":      "password",
		"wrong algorithm": strings.Replace(good, "argon2id", "argon2i", 1),
		"bad version":     strings.Replace(good, "v=19", "v=16", 1),
		"too few parts":   strings.Join(parts[:5], "$"),
		"bad salt":        strings.Join([]string{"", "argon2id", "v=19", "m=65536,t=3,p=2", "!!!", parts[5]}, "$"),
		"empty key":       strings.Join([]string{"", "argon2id", "v=19", "m=65536,t=3,p=2", parts[4], ""}, "$"),
		"huge memory":     strings.Join([]string{"", "argon2id", "v=19", "m=4294967295,t=3,p=2", parts[4], parts[5]}, "$"),
		"zero iterations": strings.Join([]string{"", "argon2id", "v=19", "m=65536,t=0,p=2", parts[4], parts[5]}, "$"),
	}
	for name, h := range cases {
		t.Run(name, func(t *testing.T) {
			if Verify("a-reasonable-password", h) {
				t.Error("malformed hash verified")
			}
		})
	}
}

func TestCheckPolicy(t *testing.T) {
	ok := []string{"twelve chars", "a much longer passphrase than needed", strings.Repeat("x", MaxLength)}
	for _, pw := range ok {
		if err := Check(pw, "me@example.com"); err != nil {
			t.Errorf("Check(%q) = %v", pw, err)
		}
	}
	bad := map[string]string{
		"too short":     "short",
		"11 chars":      "elevenchars",
		"too long":      strings.Repeat("x", MaxLength+1),
		"is the email":  "Me@Example.com",
		"only spaces":   strings.Repeat(" ", 14),
		"empty":         "",
		"short unicode": "пароль",
	}
	for name, pw := range bad {
		t.Run(name, func(t *testing.T) {
			email := "me@example.com"
			if err := Check(pw, email); !errors.Is(err, ErrPolicy) {
				t.Errorf("Check(%q) = %v, want ErrPolicy", pw, err)
			}
		})
	}
	// Length counts characters, not bytes.
	if err := Check(strings.Repeat("é", MinLength), "x@y.z"); err != nil {
		t.Errorf("12 multibyte characters rejected: %v", err)
	}
}
