package textutil

import "strings"

// NormalizeLineEndings converts CRLF and bare CR line endings to LF.
func NormalizeLineEndings(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	return strings.ReplaceAll(s, "\r", "\n")
}

// SplitLines normalizes line endings before splitting on LF.
func SplitLines(s string) []string {
	return strings.Split(NormalizeLineEndings(s), "\n")
}
