// Package berr defines business errors and their HTTP status mapping.
package berr

import "fmt"

// Code classifies a business error.
type Code string

const (
	CodeNotFound     Code = "not_found"
	CodeConflict     Code = "conflict"
	CodeInvalid      Code = "invalid"
	CodeStateConflict Code = "state_conflict"
	CodeInvariant    Code = "invariant"
	CodeInternal     Code = "internal"
)

// Error is a typed business error carrying a code and message.
type Error struct {
	Code Code
	Msg  string
}

func (e *Error) Error() string { return fmt.Sprintf("%s: %s", e.Code, e.Msg) }

// New constructs a business error.
func New(code Code, msg string) *Error { return &Error{Code: code, Msg: msg} }

// NotFoundf is a not-found error formatter.
func NotFoundf(format string, a ...any) *Error {
	return &Error{Code: CodeNotFound, Msg: fmt.Sprintf(format, a...)}
}

// Invalidf is an invalid-input error formatter.
func Invalidf(format string, a ...any) *Error {
	return &Error{Code: CodeInvalid, Msg: fmt.Sprintf(format, a...)}
}

// Conflictf is a generic conflict formatter.
func Conflictf(format string, a ...any) *Error {
	return &Error{Code: CodeConflict, Msg: fmt.Sprintf(format, a...)}
}

// Statef is a state-machine conflict (e.g. min_up/min_down violated, locked period mutated).
func Statef(format string, a ...any) *Error {
	return &Error{Code: CodeStateConflict, Msg: fmt.Sprintf(format, a...)}
}

// Invariantf is an engine invariant violation (e.g. no slack bus).
func Invariantf(format string, a ...any) *Error {
	return &Error{Code: CodeInvariant, Msg: fmt.Sprintf(format, a...)}
}

// HTTPStatus maps a code to an HTTP status code.
func (c Code) HTTPStatus() int {
	switch c {
	case CodeNotFound:
		return 404
	case CodeInvalid:
		return 400
	case CodeConflict, CodeStateConflict:
		return 409
	case CodeInvariant:
		return 422
	default:
		return 500
	}
}

// Wrap converts any error into a berr.Error, preserving existing codes.
func Wrap(err error) *Error {
	if err == nil {
		return nil
	}
	if e, ok := err.(*Error); ok {
		return e
	}
	return &Error{Code: CodeInternal, Msg: err.Error()}
}
