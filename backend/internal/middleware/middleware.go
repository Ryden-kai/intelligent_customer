// Package middleware assembles the HTTP middlewares used across the API:
// request id, structured access log, panic recovery, CORS, and JWT auth.
package middleware

import (
	"context"
	"errors"
	"net/http"
	"runtime/debug"
	"strings"
	"time"

	"github.com/go-chi/cors"
	"github.com/google/uuid"
	"github.com/rs/zerolog"

	"intelligent_customer/backend/internal/apperr"
	"intelligent_customer/backend/internal/auth"
	"intelligent_customer/backend/internal/log"
)

const headerRequestID = "X-Request-Id"

// RequestID injects a stable id per request and echoes it in the response.
func RequestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get(headerRequestID)
		if id == "" {
			id = uuid.NewString()
		}
		w.Header().Set(headerRequestID, id)
		ctx := log.WithRequestID(r.Context(), id)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// AccessLog records method, path, status, latency, bytes, ip, ua, req_id.
// Always at INFO level; failed requests are re-emitted at WARN/ERROR so they
// stand out in development.
type statusRecorder struct {
	http.ResponseWriter
	status int
	bytes  int
}

func (s *statusRecorder) WriteHeader(code int) {
	s.status = code
	s.ResponseWriter.WriteHeader(code)
}

func (s *statusRecorder) Write(b []byte) (int, error) {
	if s.status == 0 {
		s.status = http.StatusOK
	}
	n, err := s.ResponseWriter.Write(b)
	s.bytes += n
	return n, err
}

func AccessLog(base zerolog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			rec := &statusRecorder{ResponseWriter: w}
			next.ServeHTTP(rec, r)
			dur := time.Since(start)

			l := log.With(r.Context(), base).With().
				Str("method", r.Method).
				Str("path", r.URL.Path).
				Str("query", r.URL.RawQuery).
				Int("status", rec.status).
				Int64("duration_ms", dur.Milliseconds()).
				Int("bytes", rec.bytes).
				Str("remote", clientIP(r)).
				Str("ua", r.UserAgent()).
				Logger()

			switch {
			case rec.status >= 500:
				l.Error().Msg("http_request")
			case rec.status >= 400:
				l.Warn().Msg("http_request")
			default:
				l.Info().Msg("http_request")
			}
		})
	}
}

// Recover turns panics into a 500 + structured JSON log including stack.
func Recover(base zerolog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				if rec := recover(); rec != nil {
					l := log.With(r.Context(), base)
					l.Error().
						Interface("panic", rec).
						Bytes("stack", debug.Stack()).
						Msg("panic_recovered")
					writeAppError(w, r, apperr.Internal("internal server error"))
				}
			}()
			next.ServeHTTP(w, r)
		})
	}
}

// CORS allows the configured origins; safe defaults for credentials = true.
func CORS(origins []string) func(http.Handler) http.Handler {
	if len(origins) == 0 {
		origins = []string{"*"}
	}
	c := cors.New(cors.Options{
		AllowedOrigins:   origins,
		AllowedMethods:   []string{"GET", "POST", "PUT", "DELETE", "OPTIONS"},
		AllowedHeaders:   []string{"Accept", "Authorization", "Content-Type", "X-Request-Id"},
		ExposedHeaders:   []string{"X-Request-Id"},
		AllowCredentials: true,
		MaxAge:           300,
	})
	return c.Handler
}

// RequireAuth verifies the Bearer JWT and stuffs the username into ctx.
// Handlers down the chain read it with auth.FromContext.
func RequireAuth(issuer *auth.Issuer, base zerolog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			tok := extractBearer(r.Header.Get("Authorization"))
			if tok == "" {
				writeAppError(w, r, apperr.Unauthorized("missing bearer token"))
				return
			}
			c, err := issuer.Parse(tok)
			if err != nil {
				log.With(r.Context(), base).Warn().
					Err(err).
					Msg("auth_invalid_token")
				writeAppError(w, r, apperr.Unauthorized("invalid token"))
				return
			}
			ctx := auth.WithClaims(r.Context(), c)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

func extractBearer(h string) string {
	const prefix = "Bearer "
	if strings.HasPrefix(h, prefix) {
		return strings.TrimSpace(h[len(prefix):])
	}
	return ""
}

// ----------------------------------------------------------------------------
// Response helpers shared with handlers
// ----------------------------------------------------------------------------

func writeAppError(w http.ResponseWriter, r *http.Request, err error) {
	if ae, ok := apperr.As(err); ok {
		writeJSON(w, ae.HTTPStatus, ae)
		return
	}
	// Unexpected error: fall back to a 500.
	if errors.Is(err, context.Canceled) {
		writeJSON(w, http.StatusRequestTimeout, apperr.New(http.StatusRequestTimeout, "timeout", "request canceled"))
		return
	}
	writeJSON(w, http.StatusInternalServerError, apperr.Internal("unexpected error"))
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = jsonEncode(w, v)
}

func clientIP(r *http.Request) string {
	return ClientIP(r)
}

// ClientIP 提取请求的客户端 IP：X-Forwarded-For > X-Real-IP > RemoteAddr。
//
// 公开供 audit 包使用（audit logger 需要在事件里记录 IP）。
func ClientIP(r *http.Request) string {
	if v := r.Header.Get("X-Forwarded-For"); v != "" {
		if i := strings.Index(v, ","); i >= 0 {
			return strings.TrimSpace(v[:i])
		}
		return strings.TrimSpace(v)
	}
	if v := r.Header.Get("X-Real-IP"); v != "" {
		return strings.TrimSpace(v)
	}
	// Strip port from Host
	host := r.RemoteAddr
	if i := strings.LastIndex(host, ":"); i >= 0 {
		host = host[:i]
	}
	return host
}