package main

import "fmt"

// newUserErrorf creates a formatted error for display to the user.
func newUserErrorf(format string, a ...any) error {
	return fmt.Errorf(format, a...)
}

// modsError is a wrapper around an error that adds additional context.
type modsError struct {
	err    error
	reason string
}

func (m modsError) Error() string {
	return m.err.Error()
}

func (m modsError) Reason() string {
	return m.reason
}
