package handler

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/rs/zerolog"

	"intelligent_customer/backend/internal/apperr"
	"intelligent_customer/backend/internal/audit"
	"intelligent_customer/backend/internal/auth"
	"intelligent_customer/backend/internal/log"
	"intelligent_customer/backend/internal/repo"
	"intelligent_customer/backend/internal/security"
	"intelligent_customer/backend/internal/service"
)

type Admin struct {
	Service *service.Admin
	Logger  zerolog.Logger
	Auth    *AuthHandlers
	Issuer  *auth.Issuer

	// AuditSink 可选；写 auth.login 审计事件。
	AuditSink audit.Emitter
}

// ListConversations: GET /api/admin/conversations?status=&page=&size=
func (h *Admin) ListConversations(w http.ResponseWriter, r *http.Request) {
	lg := log.With(r.Context(), h.Logger).With().Str("endpoint", "admin.list_conversations").Logger()
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	if page < 1 {
		page = 1
	}
	size, _ := strconv.Atoi(r.URL.Query().Get("size"))
	if size < 1 || size > 200 {
		size = 50
	}
	res, err := h.Service.ListConversations(r.Context(), service.ListConversationsParams{
		Status: r.URL.Query().Get("status"),
		Limit:  size,
		Offset: (page - 1) * size,
	})
	if err != nil {
		lg.Error().Err(err).Msg("admin_list_failed")
		writeAppError(w, r, err)
		return
	}
	lg.Debug().Int("total", res.Total).Int("returned", len(res.Items)).Msg("admin_list_ok")
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(res)
}

// GetConversation: GET /api/admin/conversations/{id}
func (h *Admin) GetConversation(w http.ResponseWriter, r *http.Request) {
	lg := log.With(r.Context(), h.Logger).With().Str("endpoint", "admin.get_conversation").Logger()
	id := chi.URLParam(r, "id")
	res, err := h.Service.GetConversation(r.Context(), id)
	if err != nil {
		lg.Error().Err(err).Str("conv_id", id).Msg("admin_get_conv_failed")
		writeAppError(w, r, err)
		return
	}
	lg.Debug().Str("conv_id", id).Int("messages", len(res.Messages)).Msg("admin_get_conv_ok")
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(res)
}

// Stats: GET /api/admin/stats/satisfaction?days=7
func (h *Admin) StatsSatisfaction(w http.ResponseWriter, r *http.Request) {
	lg := log.With(r.Context(), h.Logger).With().Str("endpoint", "admin.stats_satisfaction").Logger()
	days, _ := strconv.Atoi(r.URL.Query().Get("days"))
	if days <= 0 {
		days = 7
	}
	res, err := h.Service.AggregateSatisfaction(r.Context(), days)
	if err != nil {
		lg.Error().Err(err).Msg("admin_stats_failed")
		writeAppError(w, r, err)
		return
	}
	lg.Debug().Int("total", res.Total).Float64("average", res.Average).Msg("admin_stats_ok")
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(res)
}

// PostAgentReply: POST /api/admin/conversations/{id}/reply
func (h *Admin) PostAgentReply(w http.ResponseWriter, r *http.Request) {
	lg := log.With(r.Context(), h.Logger).With().Str("endpoint", "admin.post_agent_reply").Logger()
	id := chi.URLParam(r, "id")
	var body struct {
		Content string `json:"content"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<14)).Decode(&body); err != nil {
		lg.Warn().Err(err).Msg("admin_reply_decode_failed")
		writeAppError(w, r, apperr.BadRequest("invalid json body").WithCause(err))
		return
	}
	m, err := h.Service.AppendAgentReply(r.Context(), id, body.Content)
	if err != nil {
		var ae *apperr.AppError
		if errors.As(err, &ae) {
			lg.Warn().
				Err(err).
				Str("code", string(ae.Code)).
				Int("http", ae.HTTPStatus).
				Msg("admin_reply_warn")
		} else {
			lg.Error().Err(err).Msg("admin_reply_error")
		}
		writeAppError(w, r, err)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	lg.Info().Str("conv_id", id).Str("msg_id", m.ID).Msg("admin_reply_ok")
	_ = json.NewEncoder(w).Encode(m)
}

// ----------------------------------------------------------------------------
// Auth (admin login) — username/password are stored in admin_users; the
// plaintext never lives in env vars or memory longer than the request.
// ----------------------------------------------------------------------------

type AuthHandlers struct {
	Issuer     *auth.Issuer
	Admins     *repo.AdminUsers
	Logger     zerolog.Logger

	// AuditSink 可选；auth.login 审计埋点。
	AuditSink audit.Emitter
}

type loginReq struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

type loginResp struct {
	Token     string `json:"token"`
	ExpiresIn int64  `json:"expiresIn"`
	Username  string `json:"username"`
	Role      string `json:"role"`
}

// Login verifies the supplied credentials against admin_users using
// Argon2id. We look up by username first (cheap), then verify the hash in
// constant-time-ish fashion. last_login_at is updated on success only.
func (a *AuthHandlers) Login(w http.ResponseWriter, r *http.Request) {
	lg := log.With(r.Context(), a.Logger).With().Str("endpoint", "admin.login").Logger()
	var body loginReq
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<14)).Decode(&body); err != nil {
		lg.Warn().Err(err).Msg("login_decode_failed")
		writeAppError(w, r, apperr.BadRequest("invalid json body").WithCause(err))
		return
	}
	if body.Username == "" || body.Password == "" {
		writeAppError(w, r, apperr.Unauthorized("invalid credentials"))
		return
	}

	u, err := a.Admins.GetByUsername(r.Context(), body.Username)
	if err != nil {
		// Same response for unknown user and wrong password — avoid
		// revealing which side failed.
		if errors.Is(err, repo.ErrNotFound) {
			lg.Warn().Str("user", body.Username).Msg("login_rejected_unknown")
			writeAppError(w, r, apperr.Unauthorized("invalid credentials"))
			return
		}
		lg.Error().Err(err).Msg("login_lookup_failed")
		writeAppError(w, r, apperr.Internal("lookup admin").WithCause(err))
		return
	}

	ok, err := security.VerifyPassword(u.PasswordHash, body.Password)
	if err != nil {
		// Treat hash corruption as 500, not 401, so we don't mask a DB
		// problem as a credential problem.
		lg.Error().Err(err).Str("user", u.Username).Msg("login_verify_error")
		writeAppError(w, r, apperr.Internal("verify password").WithCause(err))
		return
	}
	if !ok {
		lg.Warn().Str("user", body.Username).Msg("login_rejected_bad_password")
		writeAppError(w, r, apperr.Unauthorized("invalid credentials"))
		return
	}

	if err := a.Admins.TouchLastLogin(r.Context(), u.ID); err != nil {
		// Non-fatal: log and continue. The login still succeeds.
		lg.Warn().Err(err).Str("user", u.Username).Msg("login_touch_failed")
	}

	tok, err := a.Issuer.Sign(u.Username, u.Role)
	if err != nil {
		lg.Error().Err(err).Msg("login_sign_failed")
		writeAppError(w, r, apperr.Internal("sign token").WithCause(err))
		return
	}
	// 审计：登录成功。
	if a.AuditSink != nil {
		a.AuditSink.EmitFromRequest(r, audit.ActionAuthLogin, "user", u.ID,
			audit.Payload{"username": u.Username, "role": u.Role})
	}
	lg.Info().Str("user", u.Username).Msg("login_ok")
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(loginResp{
		Token:     tok,
		ExpiresIn: int64(a.Issuer.TTL().Seconds()),
		Username:  u.Username,
		Role:      u.Role,
	})
}

func (a *AuthHandlers) Whoami(w http.ResponseWriter, r *http.Request) {
	c, ok := auth.FromContext(r.Context())
	if !ok {
		writeAppError(w, r, apperr.Unauthorized("missing claims"))
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"username":    c.Username,
		"role":        c.Role,
		"email":       c.Email,
		"permissions": c.Permissions,
		"tenant_id":   c.TenantID,
	})
}

// shared error writer used by all handlers (also defined in middleware pkg).
func writeAppError(w http.ResponseWriter, r *http.Request, err error) {
	// Inline copy of middleware.writeAppError so handlers don't depend on the
	// middleware package internals. Keep behaviour identical.
	if ae, ok := apperr.As(err); ok {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(ae.HTTPStatus)
		_ = json.NewEncoder(w).Encode(ae)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusInternalServerError)
	_ = json.NewEncoder(w).Encode(apperr.Internal("unexpected error"))
}