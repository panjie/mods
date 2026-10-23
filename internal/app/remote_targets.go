package app

import (
	"regexp"
	"strings"
)

var reviewURLStartRe = regexp.MustCompile(`(?i)[a-z][a-z0-9+.-]*://`)

// Redaction is a display concern; it must preserve adjacent executable syntax.
func redactRemoteURLsForDisplay(value string) string {
	var out strings.Builder
	last := 0
	quote, quoteStart, scanned := byte(0), -1, 0
	for _, start := range reviewURLStartRe.FindAllStringIndex(value, -1) {
		if start[0] < last {
			continue
		}
		// Track actual opening quotes, not just the preceding character:
		// ''https://host/?q=x&command has an executable '&'. Escaped quotes
		// and quotes inside another string do not open a literal URL either.
		for scanned < start[0] {
			c := value[scanned]
			if (c == '\\' || c == '`') && quote != '\'' {
				scanned += 2
				continue
			}
			if quote == 0 && (c == '\'' || c == '"') {
				quote, quoteStart = c, scanned
			} else if c == quote {
				quote = 0
			}
			scanned++
		}
		quotedURL := quote != 0 && quoteStart == start[0]-1
		end := start[1]
		for end < len(value) {
			c := value[end]
			if strings.ContainsRune(" \t\r\n\"'<>;|()`$\\", rune(c)) ||
				c == '&' && (!quotedURL || end+1 < len(value) && value[end+1] == '&') {
				break
			}
			end++
		}
		out.WriteString(value[last:start[0]])
		out.WriteString(redactReviewURL(value[start[0]:end]))
		last = end
	}
	out.WriteString(value[last:])
	return out.String()
}
func redactReviewURL(raw string) string {
	scheme := strings.Index(raw, "://")
	if scheme < 0 {
		return raw
	}
	authorityEnd := len(raw)
	if i := strings.IndexAny(raw[scheme+3:], "/?#"); i >= 0 {
		authorityEnd = scheme + 3 + i
	}
	if at := strings.LastIndex(raw[scheme+3:authorityEnd], "@"); at >= 0 {
		raw = raw[:scheme+3] + "[redacted]@" + raw[scheme+3+at+1:]
	}
	base, fragment, hasFragment := strings.Cut(raw, "#")
	path, query, hasQuery := strings.Cut(base, "?")
	if hasQuery {
		base = path + "?"
		if query != "" {
			base += "[redacted]"
		}
	}
	if hasFragment {
		base += "#"
		if fragment != "" {
			base += "[redacted]"
		}
	}
	return base
}
