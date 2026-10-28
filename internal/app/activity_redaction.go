package app

import (
	"encoding/json"
	"regexp"

	"github.com/charmbracelet/x/ansi"
)

// Activity is reference text, not executable source: an ampersand belongs to
// the displayed URL query here. Review source retains its shell-aware parser.
var activityURL = regexp.MustCompile(`(?i)[a-z][a-z0-9+.-]*://[^\s"'<>]+`)

func (m *Mods) redactActivityText(text string) string {
	if m.secrets != nil {
		text = m.secrets.Redact(text)
	}
	return reviewLiteral(ansi.Strip(activityURL.ReplaceAllStringFunc(text, redactReviewURL)))
}

// Decode before redacting so JSON escapes cannot conceal a known secret.
// These arguments are display copies and never reach execution or storage.
func (m *Mods) activityDisplayArgs(data []byte) []byte {
	var value any
	if json.Unmarshal(data, &value) != nil {
		return []byte(m.redactActivityText(string(data)))
	}
	var walk func(any) any
	walk = func(v any) any {
		switch v := v.(type) {
		case string:
			return m.redactActivityText(v)
		case map[string]any:
			for key, child := range v {
				v[key] = walk(child)
			}
		case []any:
			for i, child := range v {
				v[i] = walk(child)
			}
		}
		return v
	}
	result, _ := json.Marshal(walk(value))
	return result
}

type activityDisplayError struct {
	error
	text string
}

func (e activityDisplayError) Error() string { return e.text }
func (e activityDisplayError) Unwrap() error { return e.error }

func (m *Mods) activityStatus(name string, data []byte, err error, width int) string {
	if err != nil {
		err = activityDisplayError{err, m.redactActivityText(err.Error())}
	}
	return ToolResultStatus(name, m.activityDisplayArgs(data), err, width)
}
