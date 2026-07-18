package textutil

import "unicode/utf8"

// TruncateUTF8Bytes limits s to maxBytes without returning a partial UTF-8
// encoding. The byte-based limit is preserved for callers that use it as an
// I/O or request-size budget.
func TruncateUTF8Bytes(s string, maxBytes int) string {
	if maxBytes <= 0 {
		return ""
	}
	if len(s) <= maxBytes {
		return s
	}
	end := maxBytes
	for end > 0 && !utf8.RuneStart(s[end]) {
		end--
	}
	return s[:end]
}

// ValidUTF8Prefix removes an incomplete or otherwise invalid suffix. It is
// useful when a streaming byte buffer is capped between two writes.
func ValidUTF8Prefix(s string) string {
	for len(s) > 0 && !utf8.ValidString(s) {
		s = s[:len(s)-1]
	}
	return s
}
