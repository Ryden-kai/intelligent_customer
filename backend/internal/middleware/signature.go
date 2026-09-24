package middleware

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/rs/zerolog"

	"intelligent_customer/backend/internal/apperr"
	"intelligent_customer/backend/internal/log"
	"intelligent_customer/backend/internal/repo"
	"intelligent_customer/backend/internal/security"
)

// SignatureOptions wires the HMAC verification middleware.
type SignatureOptions struct {
	Secret       []byte
	Nonces       *repo.Nonces
	Logger       zerolog.Logger
	ClockSkew    time.Duration
	NonceTTL     time.Duration
	Required     bool // false → middleware is a no-op (development convenience)
}

// RequireSignature verifies HMAC + timestamp + nonce for every request.
// When Required is false the middleware is a no-op so the API can be used
// during local smoke tests without having to plumb signing through curl.
func RequireSignature(opts SignatureOptions) func(http.Handler) http.Handler {
	if !opts.Required {
		return func(next http.Handler) http.Handler { return next }
	}
	if opts.ClockSkew <= 0 {
		opts.ClockSkew = security.DefaultMaxClockSkew
	}
	if opts.NonceTTL <= 0 {
		opts.NonceTTL = opts.ClockSkew + time.Minute
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Drain the body so handlers downstream can still read it.
			body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
			if err != nil {
				writeAppError(w, r, apperr.BadRequest("could not read request body"))
				return
			}
			r.Body = io.NopCloser(bytes.NewReader(body))

			fields, err := readHeaders(r)
			if err != nil {
				log.With(r.Context(), opts.Logger).Warn().Err(err).Msg("sig_missing_headers")
				writeAppError(w, r, apperr.Unauthorized("missing signature headers").WithCause(err))
				return
			}

			now := time.Now()
			if verr := security.Verify(opts.Secret, r.Method, r.URL.RequestURI(),
				fields.ts, fields.nonce, fields.sig, body, now, opts.ClockSkew); verr != nil {
				log.With(r.Context(), opts.Logger).Warn().Err(verr).Msg("sig_verify_failed")
				writeAppError(w, r, apperr.Unauthorized("invalid signature").WithCause(verr))
				return
			}

			// Anti-replay: register the nonce once. UNIQUE on the table
			// makes InsertOnce an atomic "have I seen this before" probe.
			expires := now.Add(opts.NonceTTL).UnixMilli()
			ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
			defer cancel()
			fresh, err := opts.Nonces.InsertOnce(ctx, fields.nonce, expires)
			if err != nil {
				log.With(r.Context(), opts.Logger).Error().Err(err).Msg("sig_nonce_store_failed")
				writeAppError(w, r, apperr.Internal("nonce store").WithCause(err))
				return
			}
			if !fresh {
				log.With(r.Context(), opts.Logger).Warn().
					Str("nonce", fields.nonce).Msg("sig_replay_detected")
				writeAppError(w, r, apperr.Unauthorized("nonce reused (replay detected)"))
				return
			}
			// Best-effort prune — ignore failure.
			go func(cutoff int64) {
				_, _ = opts.Nonces.PruneExpired(context.Background(),
					time.UnixMilli(cutoff))
			}(now.Add(-opts.NonceTTL).UnixMilli())

			next.ServeHTTP(w, r)
		})
	}
}

// requestFields mirrors security.requestFields; duplicated here so this
// middleware doesn't reach into security's private symbols.
type sigFields struct {
	ts    int64
	nonce string
	sig   string
}

func readHeaders(r *http.Request) (sigFields, error) {
	tsStr := strings.TrimSpace(r.Header.Get(security.HeaderTimestamp))
	if tsStr == "" {
		return sigFields{}, errors.New("missing X-Timestamp")
	}
	ts, err := strconv.ParseInt(tsStr, 10, 64)
	if err != nil {
		return sigFields{}, fmt.Errorf("invalid X-Timestamp: %w", err)
	}
	nonce := strings.TrimSpace(r.Header.Get(security.HeaderNonce))
	if nonce == "" {
		return sigFields{}, errors.New("missing X-Nonce")
	}
	sig := strings.TrimSpace(r.Header.Get(security.HeaderSignature))
	if sig == "" {
		return sigFields{}, errors.New("missing X-Signature")
	}
	return sigFields{ts: ts, nonce: nonce, sig: sig}, nil
}
