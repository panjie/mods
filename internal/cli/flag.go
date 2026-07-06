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

func newThinkFlag(val ThinkMode, p *ThinkMode) *thinkFlag {
	*p = val
	return (*thinkFlag)(p)
}

type thinkFlag ThinkMode

func (r *thinkFlag) Set(s string) error {
	switch s {
	case "off", "on":
		*r = thinkFlag(s)
		return nil
	default:
		return fmt.Errorf("invalid think mode %q, must be off or on", s)
	}
}

func (r *thinkFlag) String() string {
	return string(*r)
}

func (*thinkFlag) Type() string {
	return "think"
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

func newFormatFlag(val string, p *string) *formatFlag {
	*p = val
	return (*formatFlag)(p)
}

type formatFlag string

func (f *formatFlag) Set(s string) error {
	*f = formatFlag(s)
	return nil
}

func (f *formatFlag) String() string {
	return string(*f)
}

func (*formatFlag) Type() string {
	return "format"
}
