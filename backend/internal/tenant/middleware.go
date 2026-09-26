package tenant

import (
	"context"
	"net/http"

	"intelligent_customer/backend/internal/auth"
)

// Middleware builds an http.Handler middleware that injects the tenant
// info into ctx for every downstream handler. v2.1 implementation:
//
//   * If the request is authenticated (JWT in Authorization header), look
//     for the `tid` claim. Empty / missing → fall back to DefaultID.
//   * Otherwise look at X-Tenant-ID header. Empty / malformed → DefaultID.
//   * If a tenant id is provided but doesn't exist in the tenants table,
//     reject with 404 — never silently invent a tenant.
//
// The validator is optional; pass nil to skip the existence check (useful
// for tests / migration windows where the tenants table may not be ready).
func Middleware(repo *Repo, issuer *auth.Issuer) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			t, err := resolveTenant(r, issuer, repo)
			if err != nil {
				http.Error(w, err.Error(), http.StatusNotFound)
				return
			}
			next.ServeHTTP(w, r.WithContext(WithTenant(r.Context(), t)))
		})
	}
}

// Resolve is the imperative variant for code paths that don't go through
// HTTP middleware (background jobs, CLI tools, NDJSON streams).
func Resolve(ctx context.Context, hint string, repo *Repo) (Info, error) {
	if hint == "" {
		return Info{ID: DefaultID, Name: "default", Region: "cn", IsDefault: true}, nil
	}
	if repo == nil {
		return Info{ID: hint}, nil
	}
	t, err := repo.Get(ctx, hint)
	if err != nil {
		return Info{}, err
	}
	return t.ToInfo(), nil
}

func resolveTenant(r *http.Request, issuer *auth.Issuer, repo *Repo) (Info, error) {
	hint := FromHeader(r.Header.Get(HeaderName))
	if issuer != nil {
		// JWT carries the canonical hint for admin users; fall back to
		// the header for anonymous routes (chat / skills / etc.).
		if c, err := bearerClaims(r, issuer); err == nil && c != nil {
			if tid, ok := c[ClaimKey].(string); ok && FromHeader(tid) != "" {
				hint = FromHeader(tid)
			}
		}
	}
	if hint == "" {
		return Info{ID: DefaultID, Name: "default", Region: "cn", IsDefault: true}, nil
	}
	if repo == nil {
		return Info{ID: hint}, nil
	}
	t, err := repo.Get(r.Context(), hint)
	if err != nil {
		return Info{}, err
	}
	return t.ToInfo(), nil
}

// bearerClaims returns the raw JWT claims map for the Authorization
// header, or nil when no token is present. Errors are swallowed because
// the absence of a valid token is the common case for /api/chat and the
// public skill endpoints — they shouldn't 401 just because no admin
// is around.
func bearerClaims(r *http.Request, issuer *auth.Issuer) (map[string]any, error) {
	tok := r.Header.Get("Authorization")
	if len(tok) < 8 || tok[:7] != "Bearer " {
		return nil, nil
	}
	claims, err := issuer.Parse(tok[7:])
	if err != nil {
		return nil, err
	}
	out := map[string]any{
		"sub":  claims.Username,
		"role": claims.Role,
	}
	return out, nil
}
