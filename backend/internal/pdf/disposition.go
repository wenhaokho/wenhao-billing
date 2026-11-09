package pdf

import (
	"fmt"
	"strings"
)

// ContentDisposition is the inline header of a PDF named filename. A plain
// ASCII name is Python's `inline; filename="<name>"`. A name with a quote, a
// backslash, a control character or any non-ASCII text (a number a PATCH set)
// would break that header, so it also gets an RFC 6266 filename* carrying the
// UTF-8 name percent-encoded (RFC 5987), and the filename fallback has those
// characters replaced by underscores (Go change 6).
func ContentDisposition(filename string) string {
	plain := true
	var fallback strings.Builder
	for _, r := range filename {
		if r < 0x20 || r > 0x7e || r == '"' || r == '\\' {
			plain = false
			fallback.WriteByte('_')
			continue
		}
		fallback.WriteRune(r)
	}
	if plain {
		return `inline; filename="` + filename + `"`
	}
	var enc strings.Builder
	for _, b := range []byte(filename) {
		switch {
		case b >= 'a' && b <= 'z', b >= 'A' && b <= 'Z', b >= '0' && b <= '9',
			strings.IndexByte("!#$&+-.^_`|~", b) >= 0:
			enc.WriteByte(b)
		default:
			fmt.Fprintf(&enc, "%%%02X", b)
		}
	}
	return `inline; filename="` + fallback.String() + `"; filename*=UTF-8''` + enc.String()
}
