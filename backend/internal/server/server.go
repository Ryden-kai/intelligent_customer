// Package server wires the HTTP router. The frontend SPA lives in a
// separate repo and is served independently (Cloudflare Pages or any
// static host). This router exposes the JSON API only.
package server

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/rs/zerolog"

	"intelligent_customer/backend/internal/auth"
	"intelligent_customer/backend/internal/handler"
	"intelligent_customer/backend/internal/middleware"
)

type Deps struct {
	Logger          zerolog.Logger
	CORSOrigins     []string
	Issuer          *auth.Issuer
	ChatHandler     *handler.Chat
	FeedbackHandler *handler.Feedback
	AdminHandler    *handler.Admin
	AuthHandlers    *handler.AuthHandlers
	SignatureOpts   *middleware.SignatureOptions
}

func New(d Deps) http.Handler {
	r := chi.NewRouter()

	// Order matters: RequestID → Recover → AccessLog → CORS.
	r.Use(middleware.RequestID)
	r.Use(middleware.Recover(d.Logger))
	r.Use(middleware.AccessLog(d.Logger))
	r.Use(middleware.CORS(d.CORSOrigins))

	// Health endpoints — public, no signing required (k8s liveness probes
	// can't sign requests).
	r.Get("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	})
	r.Get("/ready", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		_, _ = w.Write([]byte(`{"status":"ready"}`))
	})

	// Public API — gated by HMAC signature when configured.
	r.Route("/api", func(r chi.Router) {
		if d.SignatureOpts != nil {
			r.Use(middleware.RequireSignature(*d.SignatureOpts))
		}
		r.Post("/chat", d.ChatHandler.ServeHTTP)
		r.Post("/feedback", d.FeedbackHandler.ServeHTTP)

		r.Route("/admin", func(r chi.Router) {
			r.Post("/login", d.AuthHandlers.Login)
			// Auth-protected sub-tree.
			r.Group(func(r chi.Router) {
				r.Use(middleware.RequireAuth(d.Issuer, d.Logger))
				r.Get("/conversations", d.AdminHandler.ListConversations)
				r.Get("/conversations/{id}", d.AdminHandler.GetConversation)
				r.Post("/conversations/{id}/reply", d.AdminHandler.PostAgentReply)
				r.Get("/stats/satisfaction", d.AdminHandler.StatsSatisfaction)
				r.Get("/me", d.AuthHandlers.Whoami)
			})
		})
	})

	// Anything that isn't /api or /health falls through to a 404. The
	// frontend is served by Cloudflare Pages (or any static host), not here.
	r.Get("/*", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"code":"not_found","message":"this is the API service; the SPA is hosted separately"}`))
	})
	return r
}

// ListenOptions captures everything needed to bind a server: addr + optional
// TLS material. When both TLS files are present we run ListenAndServeTLS,
// otherwise plain HTTP (development convenience).
type ListenOptions struct {
	Addr        string
	ReadTO      time.Duration
	WriteTO     time.Duration
	TLSCertFile string
	TLSKeyFile  string
}

// ListenAndServe runs an HTTP/HTTPS server with graceful shutdown. When
// TLSConfig is nil the server is plain HTTP; otherwise ListenAndServeTLS is
// used.
func ListenAndServe(ctx context.Context, opts ListenOptions, h http.Handler, logger zerolog.Logger) error {
	srv := &http.Server{
		Addr:         opts.Addr,
		Handler:      h,
		ReadTimeout:  opts.ReadTO,
		WriteTimeout: opts.WriteTO,
		IdleTimeout:  120 * time.Second,
	}
	useTLS := opts.TLSCertFile != "" && opts.TLSKeyFile != ""
	if useTLS {
		logger.Info().Str("addr", opts.Addr).Str("cert", opts.TLSCertFile).Msg("https_server_listen")
	} else {
		logger.Warn().
			Str("addr", opts.Addr).
			Msg("http_server_listen_no_tls_set_TLS_CERT_FILE_and_TLS_KEY_FILE_to_enable_https")
	}
	errCh := make(chan error, 1)
	go func() {
		var err error
		if useTLS {
			err = srv.ListenAndServeTLS(opts.TLSCertFile, opts.TLSKeyFile)
		} else {
			err = srv.ListenAndServe()
		}
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
			return
		}
		errCh <- nil
	}()
	select {
	case <-ctx.Done():
		logger.Info().Msg("http_server_shutdown")
		shCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return srv.Shutdown(shCtx)
	case err := <-errCh:
		return err
	}
}