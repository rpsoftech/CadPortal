package service

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/rpsoftech/cad-portal/backend/internal/db"
)

// codeDigits is the minimum width of the number in a design code: LR0001. A category that
// passes 9999 designs simply gets five digits.
const codeDigits = 4

var prefixRE = regexp.MustCompile(`^[A-Z]{1,6}$`)

// normalizePrefix upper-cases and checks a category code prefix. Empty means "not set yet".
func normalizePrefix(p string) (string, error) {
	p = strings.ToUpper(strings.TrimSpace(p))
	if p != "" && !prefixRE.MatchString(p) {
		return "", invalid("code prefix must be 1 to 6 letters, for example LR")
	}
	return p, nil
}

// FormatCode builds a design code from a category prefix and number.
func FormatCode(prefix string, n int32) string {
	return fmt.Sprintf("%s%0*d", prefix, codeDigits, n)
}

// allocateCode hands out the next free design code of a category. It must run in the same
// transaction as the design insert: the category row is locked until commit, so two creators
// never get the same number, and a rollback returns the number. Codes that are already taken
// (by hand-made designs from before numbering, or an earlier prefix) are skipped.
func allocateCode(ctx context.Context, q *db.Queries, categoryID int64) (string, error) {
	for range 10000 {
		c, err := q.NextCategoryNumber(ctx, categoryID)
		if errors.Is(err, pgx.ErrNoRows) {
			return "", invalid("unknown category")
		}
		if err != nil {
			return "", err
		}
		if !c.CodePrefix.Valid || c.CodePrefix.String == "" {
			return "", invalid("category %q has no code prefix yet: an admin sets one in Master data", c.Name)
		}
		if !prefixRE.MatchString(c.CodePrefix.String) {
			return "", invalid("category %q has an invalid code prefix %q: an admin fixes it in Master data", c.Name, c.CodePrefix.String)
		}
		code := FormatCode(c.CodePrefix.String, c.NextSeq)
		taken, err := q.DesignCodeTaken(ctx, code)
		if err != nil {
			return "", err
		}
		if !taken {
			return code, nil
		}
	}
	return "", errors.New("no free design code found for this category")
}
