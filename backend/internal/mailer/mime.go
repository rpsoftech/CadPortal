package mailer

import (
	"encoding/base64"
	"strings"
)

// mimeEncode makes an RFC 2047 encoded word for a subject with non-ASCII characters.
func mimeEncode(s string) string {
	return "=?UTF-8?B?" + base64.StdEncoding.EncodeToString([]byte(strings.TrimSpace(s))) + "?="
}
