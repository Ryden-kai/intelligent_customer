package auth

import "context"

type ctxKey struct{ name string }

var claimsKey = ctxKey{"claims"}

func WithClaims(ctx context.Context, c *Claims) context.Context {
	return context.WithValue(ctx, claimsKey, c)
}

func FromContext(ctx context.Context) (*Claims, bool) {
	v, ok := ctx.Value(claimsKey).(*Claims)
	return v, ok
}