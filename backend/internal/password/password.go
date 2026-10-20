// Package password hashes and verifies passwords with Argon2id and enforces the
// password policy. Hashes are stored as PHC strings, so the parameters travel
// with each hash and can be raised later without breaking existing ones.
package password

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"golang.org/x/crypto/argon2"
)

// Parameters follow the OWASP recommendation for Argon2id (m=64 MiB, t=3, p=2).
const (
	memoryKiB  = 64 * 1024
	iterations = 3
	threads    = 2
	saltLen    = 16
	keyLen     = 32

	MinLength = 12
	MaxLength = 128
)

var b64 = base64.RawStdEncoding

// ErrPolicy wraps every policy violation so callers can report it as bad input.
var ErrPolicy = errors.New("password does not meet the policy")

// Check enforces the password policy. The email is passed so a password cannot
// simply be the account's own email address.
func Check(pw, email string) error {
	n := utf8.RuneCountInString(pw)
	switch {
	case n < MinLength:
		return fmt.Errorf("%w: at least %d characters", ErrPolicy, MinLength)
	case n > MaxLength:
		return fmt.Errorf("%w: at most %d characters", ErrPolicy, MaxLength)
	case strings.EqualFold(pw, email):
		return fmt.Errorf("%w: must not be your email address", ErrPolicy)
	case strings.TrimSpace(pw) == "":
		return fmt.Errorf("%w: must not be blank", ErrPolicy)
	}
	return nil
}

// Hash returns a PHC-format Argon2id hash with a fresh random salt.
func Hash(pw string) (string, error) {
	salt := make([]byte, saltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	key := argon2.IDKey([]byte(pw), salt, iterations, memoryKiB, threads, keyLen)
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, memoryKiB, iterations, threads, b64.EncodeToString(salt), b64.EncodeToString(key)), nil
}

// Verify checks pw against a PHC hash in constant time. Any malformed hash
// simply fails verification.
func Verify(pw, encoded string) bool {
	parts := strings.Split(encoded, "$")
	// ["", "argon2id", "v=19", "m=..,t=..,p=..", salt, key]
	if len(parts) != 6 || parts[1] != "argon2id" {
		return false
	}
	var version int
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil || version != argon2.Version {
		return false
	}
	var m, t uint32
	var p uint8
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &m, &t, &p); err != nil {
		return false
	}
	// Refuse absurd parameters from a corrupted row instead of allocating them.
	if m == 0 || m > 1<<20 || t == 0 || t > 10 || p == 0 || p > 16 {
		return false
	}
	salt, err := b64.DecodeString(parts[4])
	if err != nil || len(salt) == 0 {
		return false
	}
	want, err := b64.DecodeString(parts[5])
	if err != nil || len(want) == 0 {
		return false
	}
	got := argon2.IDKey([]byte(pw), salt, t, m, p, uint32(len(want)))
	return subtle.ConstantTimeCompare(got, want) == 1
}
