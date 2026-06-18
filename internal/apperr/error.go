package apperr

import "fmt"

// newUserErrorf creates a formatted error for display to the user.
func NewUserErrorf(format string, a ...any) error {
	return fmt.Errorf(format, a...)
}

// Error is a wrapper around an error that adds additional context.
type Error struct {
	Err        error
	ReasonText string
}

func (m Error) Error() string {
	if m.Err == nil {
		return m.ReasonText
	}
	return m.Err.Error()
}

func (m Error) Reason() string {
	return m.ReasonText
}

func (m Error) Unwrap() error {
	return m.Err
}
