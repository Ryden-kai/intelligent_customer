package apperr

import (
	"errors"
	"net/http"
	"testing"
)

func TestConstructors(t *testing.T) {
	cases := []struct {
		err      *AppError
		status   int
		code     Code
	}{
		{BadRequest("x"), http.StatusBadRequest, CodeBadRequest},
		{Unauthorized("x"), http.StatusUnauthorized, CodeUnauthorized},
		{Forbidden("x"), http.StatusForbidden, CodeForbidden},
		{NotFound("x"), http.StatusNotFound, CodeNotFound},
		{Conflict("x"), http.StatusConflict, CodeConflict},
		{Unprocessable("x"), http.StatusUnprocessableEntity, CodeUnprocessable},
		{Upstream("x"), http.StatusBadGateway, CodeUpstream},
		{RateLimited("x"), http.StatusTooManyRequests, CodeRateLimited},
		{Internal("x"), http.StatusInternalServerError, CodeInternal},
	}
	for _, c := range cases {
		if c.err.HTTPStatus != c.status {
			t.Fatalf("status mismatch: %d want %d", c.err.HTTPStatus, c.status)
		}
		if c.err.Code != c.code {
			t.Fatalf("code mismatch: %s want %s", c.err.Code, c.code)
		}
	}
}

func TestWithCauseUnwraps(t *testing.T) {
	base := Upstream("jev failed")
	root := errors.New("network")
	wrapped := base.WithCause(root)
	if !errors.Is(wrapped, root) {
		t.Fatalf("expected errors.Is to walk to root")
	}
}

func TestWithDetail(t *testing.T) {
	e := BadRequest("bad").WithDetail("hint")
	if e.Detail != "hint" {
		t.Fatalf("detail not set: %s", e.Detail)
	}
	// Mutating via WithDetail must not mutate the original.
	_ = e.WithDetail("hint2")
	if e.Detail != "hint" {
		t.Fatalf("original mutated")
	}
}

func TestAs(t *testing.T) {
	if _, ok := As(errors.New("plain")); ok {
		t.Fatalf("plain err should not be AppError")
	}
	orig := Internal("boom")
	if _, ok := As(orig); !ok {
		t.Fatalf("expected As to return ok")
	}
}