package errs

import "fmt"

// ValidationError represents a user-input validation failure.
// The message is safe to return to API consumers.
type ValidationError struct {
	msg string
}

func NewValidation(format string, args ...any) *ValidationError {
	return &ValidationError{msg: fmt.Sprintf(format, args...)}
}

func (e *ValidationError) Error() string { return e.msg }

// NotFoundError represents a resource that could not be found.
// The message is safe to return to API consumers.
type NotFoundError struct {
	msg string
}

func NewNotFound(format string, args ...any) *NotFoundError {
	return &NotFoundError{msg: fmt.Sprintf(format, args...)}
}

func (e *NotFoundError) Error() string { return e.msg }
