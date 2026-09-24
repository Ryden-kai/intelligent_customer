// Package auth issues and validates JWT tokens for admin endpoints. We use
// HS256 with the JWT_SECRET env var; secret rotation is out of scope.
package auth

import (
	"errors"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"intelligent_customer/backend/internal/apperr"
)

type Claims struct {
	Username string `json:"sub"`
	Role     string `json:"role"`
	jwt.RegisteredClaims
}

type Issuer struct {
	secret []byte
	ttl    time.Duration
}

func NewIssuer(secret string, ttl time.Duration) *Issuer {
	return &Issuer{secret: []byte(secret), ttl: ttl}
}

func (i *Issuer) Sign(username, role string) (string, error) {
	now := time.Now()
	c := Claims{
		Username: username,
		Role:     role,
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(now.Add(i.ttl)),
			IssuedAt:  jwt.NewNumericDate(now),
			Subject:   username,
		},
	}
	t := jwt.NewWithClaims(jwt.SigningMethodHS256, c)
	return t.SignedString(i.secret)
}

func (i *Issuer) Parse(token string) (*Claims, error) {
	parsed, err := jwt.ParseWithClaims(token, &Claims{}, func(t *jwt.Token) (any, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, errors.New("unexpected signing method")
		}
		return i.secret, nil
	})
	if err != nil {
		return nil, apperr.Unauthorized("invalid token").WithCause(err)
	}
	c, ok := parsed.Claims.(*Claims)
	if !ok || !parsed.Valid {
		return nil, apperr.Unauthorized("invalid claims")
	}
	return c, nil
}

// TTL exposes the configured lifetime so handlers can show it to clients.
func (i *Issuer) TTL() time.Duration { return i.ttl }