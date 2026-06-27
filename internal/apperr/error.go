package apperr

import "fmt"

// newUserErrorf creates a formatted error for display to the user.
func NewUserErrorf(format string, a ...any) error {
	return fmt.Errorf(format, a...)
}

// Error is a wrapper around an error that adds additional context.
//
// Error implements error in a way that preserves both the wrapping
// ReasonText and the underlying Err message. Previously, when Err was
// set, Error() returned only Err.Error() and the ReasonText was lost
// from any caller that simply printed the error or wrapped it again.
// Now Error() composes the two so error chains carry their context
// through repeated wrapping.
type Error struct {
	Err        error
	ReasonText string
}

func (m Error) Error() string {
	switch {
	case m.Err == nil && m.ReasonText == "":
		return ""
	case m.Err == nil:
		return m.ReasonText
	case m.ReasonText == "":
		return m.Err.Error()
	default:
		return m.ReasonText + ": " + m.Err.Error()
	}
}

func (m Error) Reason() string {
	return m.ReasonText
}

func (m Error) Unwrap() error {
	return m.Err
}
