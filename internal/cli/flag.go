package cli

import (
	"fmt"
	"regexp"
	"strings"
)

func newFlagParseError(err error) flagParseError {
	var reason, flag string
	s := err.Error()
	switch {
	case strings.HasPrefix(s, "flag needs an argument:"):
		reason = "Flag %s needs an argument."
		// pflag emits two shapes:
		//   "flag needs an argument: --api"          (long flag)
		//   "flag needs an argument: 'a' in -a"      (short flag in a cluster)
		// TrimPrefix handles arbitrary multi-hyphen long flag names; the previous
		// strings.Split(s, "-") approach broke on any flag containing a hyphen.
		rest := strings.TrimSpace(strings.TrimPrefix(s, "flag needs an argument:"))
		if idx := strings.Index(rest, " in -"); idx >= 0 {
			flag = rest[idx+len(" in "):] // short cluster, e.g. "-d"
		} else {
			flag = rest // long flag, e.g. "--api"
		}
	case strings.HasPrefix(s, "unknown flag:"):
		reason = "Unknown flag %s."
		flag = strings.TrimPrefix(s, "unknown flag: ")
	case strings.HasPrefix(s, "unknown shorthand flag:"):
		reason = "Short flag %s is missing."
		// pflag format: "unknown shorthand flag: 'z' in -xz". Capture the quoted
		// character (the actual unknown flag) rather than the first character of
		// the cluster — the previous `(-\w)` regex reported -x for input -xz.
		re := regexp.MustCompile(`unknown shorthand flag: '(\w)'`)
		parts := re.FindStringSubmatch(s)
		if len(parts) > 1 {
			flag = "-" + parts[1]
		}
	case strings.HasPrefix(s, "invalid argument"):
		reason = "Flag %s have an invalid argument."
		re := regexp.MustCompile(`invalid argument ".*" for "(.*)" flag: .*`)
		parts := re.FindStringSubmatch(s)
		if len(parts) > 1 {
			flag = parts[1]
		}
	default:
		reason = s
	}
	return flagParseError{
		err:        err,
		ReasonText: reason,
		flag:       flag,
	}
}

type flagParseError struct {
	err        error
	ReasonText string
	flag       string
}

func (f flagParseError) Error() string {
	return f.err.Error()
}

func (f flagParseError) ReasonFormat() string {
	return f.ReasonText
}

func (f flagParseError) Flag() string {
	return f.flag
}

func newReasoningFlag(val ReasoningMode, p *ReasoningMode) *reasoningFlag {
	*p = val
	return (*reasoningFlag)(p)
}

type reasoningFlag ReasoningMode

func (r *reasoningFlag) Set(s string) error {
	switch s {
	case "off", "on":
		*r = reasoningFlag(s)
		return nil
	default:
		return fmt.Errorf("invalid reasoning mode %q, must be off or on", s)
	}
}

func (r *reasoningFlag) String() string {
	return string(*r)
}

func (*reasoningFlag) Type() string {
	return "reasoning"
}

func newReviewFlag(val ReviewMode, p *ReviewMode) *reviewFlag {
	*p = val
	return (*reviewFlag)(p)
}

type reviewFlag ReviewMode

func (r *reviewFlag) Set(s string) error {
	switch s {
	case "never", "auto", "always":
		*r = reviewFlag(s)
		return nil
	default:
		return fmt.Errorf("invalid review mode %q, must be auto, always, or never", s)
	}
}

func (r *reviewFlag) String() string {
	return string(*r)
}

func (*reviewFlag) Type() string {
	return "review-mode"
}

func newThemeFlag(val string, p *string) *themeFlag {
	*p = val
	return (*themeFlag)(p)
}

type themeFlag string

func (t *themeFlag) Set(s string) error {
	switch s {
	case "charm", "catppuccin", "dracula", "base16":
		*t = themeFlag(s)
		return nil
	default:
		return fmt.Errorf("invalid theme %q, must be charm, catppuccin, dracula, or base16", s)
	}
}

func (t *themeFlag) String() string {
	return string(*t)
}

func (*themeFlag) Type() string {
	return "theme"
}
