package backup

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os/exec"
	"strings"
)

// PgDump streams a custom-format dump (pg_dump -Fc, already compressed) of the database at url.
func PgDump(url string) func(ctx context.Context, dst io.Writer) error {
	return func(ctx context.Context, dst io.Writer) error {
		var stderr bytes.Buffer
		cmd := exec.CommandContext(ctx, "pg_dump", "-Fc", "--dbname", url)
		cmd.Stdout, cmd.Stderr = dst, &stderr
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("%w: %s", err, strings.TrimSpace(redact(stderr.String(), url)))
		}
		return nil
	}
}

// PgVerify lists the contents of a dump file; it fails if the file is not a readable archive.
func PgVerify(ctx context.Context, path string) error {
	var stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, "pg_restore", "--list", path)
	cmd.Stdout, cmd.Stderr = io.Discard, &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return nil
}

// redact keeps the database password out of logs and out of the run record.
func redact(s, url string) string {
	if i := strings.Index(url, "://"); i >= 0 {
		rest := url[i+3:]
		if at := strings.LastIndex(rest, "@"); at >= 0 {
			if colon := strings.Index(rest[:at], ":"); colon >= 0 {
				if pw := rest[colon+1 : at]; pw != "" {
					s = strings.ReplaceAll(s, pw, "***")
				}
			}
		}
	}
	return s
}
