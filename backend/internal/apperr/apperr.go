// Package apperr defines a typed error model used across all services and
// surfaced verbatim by the global HTTP error middleware. Stack traces and
// internal details never leave the boundary.
package apperr

import (
	"errors"
	"fmt"
	"net/http"
)

type Code string

const (
	CodeBadRequest    Code = "bad_request"
	CodeUnauthorized  Code = "unauthorized"
	CodeForbidden     Code = "forbidden"
	CodeNotFound      Code = "not_found"
	CodeConflict      Code = "conflict"
	CodeUnprocessable Code = "unprocessable"
	CodeUpstream      Code = "upstream_error" // 3rd-party (LLM/Jev) failure
	CodeRateLimited   Code = "rate_limited"
	CodeInternal      Code = "internal"
)

type AppError struct {
	HTTPStatus int    `json:"-"`
	Code       Code   `json:"code"`
	Message    string `json:"message"`
	Detail     string `json:"detail,omitempty"`
	cause     error
}

func (e *AppError) Error() string {
	if e.cause != nil {
		return fmt.Sprintf("%s: %s: %v", e.Code, e.Message, e.cause)
	}
	return fmt.Sprintf("%s: %s", e.Code, e.Message)
}

func (e *AppError) Unwrap() error { return e.cause }

func (e *AppError) WithCause(err error) *AppError {
	cp := *e
	cp.cause = err
	return &cp
}

func (e *AppError) WithDetail(d string) *AppError {
	cp := *e
	cp.Detail = d
	return &cp
}

// Constructors ---------------------------------------------------------------

func New(status int, code Code, msg string) *AppError {
	return &AppError{HTTPStatus: status, Code: code, Message: msg}
}

func BadRequest(msg string) *AppError    { return New(http.StatusBadRequest, CodeBadRequest, msg) }
func Unauthorized(msg string) *AppError  { return New(http.StatusUnauthorized, CodeUnauthorized, msg) }
func Forbidden(msg string) *AppError     { return New(http.StatusForbidden, CodeForbidden, msg) }
func NotFound(msg string) *AppError      { return New(http.StatusNotFound, CodeNotFound, msg) }
func Conflict(msg string) *AppError      { return New(http.StatusConflict, CodeConflict, msg) }
func Unprocessable(msg string) *AppError { return New(http.StatusUnprocessableEntity, CodeUnprocessable, msg) }
func Upstream(msg string) *AppError      { return New(http.StatusBadGateway, CodeUpstream, msg) }
func RateLimited(msg string) *AppError   { return New(http.StatusTooManyRequests, CodeRateLimited, msg) }
func Internal(msg string) *AppError      { return New(http.StatusInternalServerError, CodeInternal, msg) }

// As is a convenience wrapper around errors.As.
func As(err error) (*AppError, bool) {
	var ae *AppError
	if errors.As(err, &ae) {
		return ae, true
	}
	return nil, false
}