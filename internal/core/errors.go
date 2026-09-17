// Package core holds primitives shared by every domain module: identifiers,
// money, errors and the role model. It must not import any other internal
// package so that every module can depend on it freely.
package core

import (
	"errors"
	"fmt"
)

// ErrorCode is a stable, machine readable classification of a domain error.
// The HTTP layer maps it to a status code, the Telegram layer maps it to a
// human sentence. Domain code never knows about either.
type ErrorCode string

const (
	CodeInvalid      ErrorCode = "invalid"      // 400 - request failed validation
	CodeUnauthorized ErrorCode = "unauthorized" // 401 - no / bad identity
	CodeForbidden    ErrorCode = "forbidden"    // 403 - identity lacks the role
	CodeNotFound     ErrorCode = "not_found"    // 404
	CodeConflict     ErrorCode = "conflict"     // 409 - state does not allow it
	CodeInternal     ErrorCode = "internal"     // 500
)

// Error is the single error type crossing module boundaries.
type Error struct {
	Code    ErrorCode
	Message string
	// Fields carries per-field validation messages, keyed by field name.
	Fields map[string]string
	cause  error
}

func (e *Error) Error() string {
	if e.cause != nil {
		return fmt.Sprintf("%s: %s: %v", e.Code, e.Message, e.cause)
	}
	return fmt.Sprintf("%s: %s", e.Code, e.Message)
}

func (e *Error) Unwrap() error { return e.cause }

// WithCause attaches an underlying error for logging without leaking it to the client.
func (e *Error) WithCause(err error) *Error {
	e.cause = err
	return e
}

func Invalid(format string, args ...any) *Error {
	return &Error{Code: CodeInvalid, Message: fmt.Sprintf(format, args...)}
}

func Unauthorized(format string, args ...any) *Error {
	return &Error{Code: CodeUnauthorized, Message: fmt.Sprintf(format, args...)}
}

func Forbidden(format string, args ...any) *Error {
	return &Error{Code: CodeForbidden, Message: fmt.Sprintf(format, args...)}
}

func NotFound(what string) *Error {
	return &Error{Code: CodeNotFound, Message: what + " not found"}
}

func Conflict(format string, args ...any) *Error {
	return &Error{Code: CodeConflict, Message: fmt.Sprintf(format, args...)}
}

func Internal(err error) *Error {
	return &Error{Code: CodeInternal, Message: "internal error", cause: err}
}

// CodeOf extracts the error code from any error, defaulting to internal.
func CodeOf(err error) ErrorCode {
	var e *Error
	if errors.As(err, &e) {
		return e.Code
	}
	return CodeInternal
}

// AsError returns err as a *Error, wrapping unknown errors as internal ones.
func AsError(err error) *Error {
	var e *Error
	if errors.As(err, &e) {
		return e
	}
	return Internal(err)
}
