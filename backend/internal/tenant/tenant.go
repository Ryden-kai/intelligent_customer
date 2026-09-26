// Package tenant owns the multi-tenant concept introduced in v2.1.
//
// v1 was single-tenant; v2.1 keeps that surface but adds a Tenant context
// plumbing so every query can carry a tenant_id without rewriting call
// sites. The actual data isolation (RLS / per-tenant DB) is a v2.2 concern —
// see docs/v2-multitenancy.md. For now the contract is:
//
//   * Every business table carries a tenant_id column (migration 005).
//   * Repos that need tenant isolation take an explicit Tenant parameter
//     (or read it from ctx via FromContext).
//   * HTTP requests pick up tenant_id from the X-Tenant-ID header (or
//     fall back to DefaultID when missing — keeps v1 behaviour).
//   * A "tnt_default" row always exists in the tenants table (created
//     by migration 006) so v1 data has a home.
//
// The package is intentionally tiny: type + ctx helpers + a small repo.
// Cross-tenant guards (RejectIfMismatch etc.) live alongside middleware
// in middleware.go so handler code can short-circuit early.
package tenant

import (
	"context"
	"strings"
)

// DefaultID is the synthetic tenant v1 data lives under. The seed row in
// migration 006 guarantees it always exists.
const DefaultID = "tnt_default"

// HeaderName is the HTTP header that carries the tenant id for non-admin
// endpoints. Admin endpoints will switch to JWT claims in v2.2.
const HeaderName = "X-Tenant-ID"

// ClaimKey is the JWT claim reserved for tenant_id. Issuers in v2.1 don't
// emit it yet; the JWT-aware middleware treats a missing claim as DefaultID.
const ClaimKey = "tid"

// ctxKey is unexported to prevent collisions in context.WithValue.
type ctxKey struct{}

// Info is the minimal tenant context carried in ctx. We deliberately don't
// expose config_json here — callers that need tenant-specific config
// should ask the repo.
type Info struct {
	ID       string
	Name     string
	Region   string
	IsDefault bool
}

// FromContext returns the tenant stored in ctx, falling back to a synthetic
// default-tenant Info when the ctx carries none. This keeps v1 callers
// working without forcing every layer to plumb a tenant explicitly.
func FromContext(ctx context.Context) Info {
	if v, ok := ctx.Value(ctxKey{}).(Info); ok && v.ID != "" {
		return v
	}
	return Info{ID: DefaultID, Name: "default", Region: "cn", IsDefault: true}
}

// MustFromContext behaves like FromContext but returns an error when no
// tenant is on the ctx. Use this inside repos that must not silently fall
// back (e.g. an admin handler operating on a specific tenant).
func MustFromContext(ctx context.Context) (Info, error) {
	v, ok := ctx.Value(ctxKey{}).(Info)
	if !ok || v.ID == "" {
		return Info{}, errMissing
	}
	return v, nil
}

// WithTenant returns a new ctx carrying t. Passing a zero-value Info is
// treated as "no tenant" and FromContext will return DefaultID — handy
// for tests that want to opt out of the default.
func WithTenant(ctx context.Context, t Info) context.Context {
	if t.ID == "" {
		return ctx
	}
	return context.WithValue(ctx, ctxKey{}, t)
}

// FromHeader extracts the tenant id from a raw HTTP header value. It
// trims whitespace and rejects empty / path-traversal-ish input.
// Returns "" when no usable value is present.
func FromHeader(raw string) string {
	v := strings.TrimSpace(raw)
	if v == "" || len(v) > 64 {
		return ""
	}
	// Defensive: tenants ids are alphanumeric + underscores in our seed;
	// anything else is suspicious.
	for _, r := range v {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') ||
			(r >= '0' && r <= '9') || r == '_' || r == '-' {
			continue
		}
		return ""
	}
	return v
}

// Equal reports whether two infos refer to the same tenant. Useful for
// guard checks like "request tenant must match resource tenant".
func (t Info) Equal(other Info) bool {
	return t.ID != "" && t.ID == other.ID
}

// errMissing is exported via the package sentinel below.
var errMissing = &MissingError{}

// MissingError is returned by MustFromContext when no tenant is on ctx.
type MissingError struct{}

func (e *MissingError) Error() string { return "tenant: missing tenant in context" }

// IsMissing is a small predicate so handlers can branch without importing
// errors just for one comparison.
func IsMissing(err error) bool {
	_, ok := err.(*MissingError)
	return ok
}
