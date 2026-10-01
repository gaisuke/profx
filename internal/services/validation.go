package services

import "fmt"

// ValidationError carries the field a request got wrong, so the API can tell a
// client which input to mark instead of only saying "invalid". The message stays
// in Indonesian and is written to be shown to a person; the field and reason are
// what a frontend branches on.
type ValidationError struct {
	Err    error
	Field  string
	Reason string
}

func (e *ValidationError) Error() string { return e.Err.Error() }
func (e *ValidationError) Unwrap() error { return e.Err }

// validationErrorf builds one of these without repeating the struct literal at
// every call site.
func validationErrorf(base error, field, format string, args ...any) error {
	reason := fmt.Sprintf(format, args...)
	return &ValidationError{Err: fmt.Errorf("%w: %s", base, reason), Field: field, Reason: reason}
}
