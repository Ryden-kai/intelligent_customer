package handler

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/rs/zerolog"

	"intelligent_customer/backend/internal/apperr"
	"intelligent_customer/backend/internal/audit"
	"intelligent_customer/backend/internal/log"
	"intelligent_customer/backend/internal/ratelimit"
)

// AdminRateLimit 暴露限流配置查询 / 更新 2 个端点。
//
//   GET  /api/admin/ratelimit/configs
//   PUT  /api/admin/ratelimit/configs/{id}
//
// 配置变更后调用 ReloadSink 通知 LimiterMap 立即刷新（避免热重启）。
type AdminRateLimit struct {
	Repo   *ratelimit.Repo
	Logger zerolog.Logger

	// ReloadSink 在 PUT 后被调用以让 LimiterMap 重新加载；可选。
	ReloadSink func() // no-op when nil

	// AuditSink 可选；写 ratelimit.config.update 审计。
	AuditSink audit.Emitter
}

// ListConfigs GET /api/admin/ratelimit/configs
func (h *AdminRateLimit) ListConfigs(w http.ResponseWriter, r *http.Request) {
	tenantID := r.URL.Query().Get("tenant_id")
	configs, err := h.Repo.ListAll(r.Context(), tenantID)
	if err != nil {
		writeAppError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"items": configs,
		"total": len(configs),
	})
}

// UpdateConfig PUT /api/admin/ratelimit/configs/{id}
func (h *AdminRateLimit) UpdateConfig(w http.ResponseWriter, r *http.Request) {
	lg := log.With(r.Context(), h.Logger).With().Str("endpoint", "admin.ratelimit.update").Logger()
	id := chi.URLParam(r, "id")
	var body struct {
		PerMinute   int    `json:"per_minute"`
		PerHour     int    `json:"per_hour"`
		Burst       int    `json:"burst"`
		Enabled     bool   `json:"enabled"`
		Description string `json:"description"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<14)).Decode(&body); err != nil {
		writeAppError(w, r, apperr.BadRequest("invalid json body").WithCause(err))
		return
	}
	cfg, err := h.Repo.Update(r.Context(), id, ratelimit.Config{
		PerMinute:   body.PerMinute,
		PerHour:     body.PerHour,
		Burst:       body.Burst,
		Enabled:     body.Enabled,
		Description: body.Description,
	})
	if err != nil {
		if errors.Is(err, ratelimit.ErrNotFound) {
			writeAppError(w, r, apperr.NotFound("config not found"))
			return
		}
		writeAppError(w, r, err)
		return
	}
	// 触发 LimiterMap 重新加载。
	if h.ReloadSink != nil {
		h.ReloadSink()
	}
	// 审计。
	if h.AuditSink != nil {
		h.AuditSink.Emit(r.Context(), audit.ActionRateLimitConfigUpdate, "ratelimit", id,
			audit.Payload{
				"per_minute": cfg.PerMinute,
				"per_hour":   cfg.PerHour,
				"burst":      cfg.Burst,
				"enabled":    cfg.Enabled,
				"endpoint":   cfg.Endpoint,
			})
	}
	lg.Info().
		Str("id", id).
		Str("endpoint", cfg.Endpoint).
		Bool("enabled", cfg.Enabled).
		Msg("ratelimit_config_updated")
	writeJSON(w, http.StatusOK, cfg)
}