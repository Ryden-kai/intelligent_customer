// Package handler — jev_admin.go
//
// Admin endpoints for the Jev decision layer (v2.1):
//
//   GET    /api/admin/jev/templates              list templates (per tenant)
//   GET    /api/admin/jev/templates/{id}         fetch one template
//   POST   /api/admin/jev/templates              create a new template version
//   PUT    /api/admin/jev/templates/{id}         update mutable fields
//   DELETE /api/admin/jev/templates/{id}         remove a template row
//   POST   /api/admin/jev/templates/publish/{id} flip status=published
//   POST   /api/admin/jev/templates/archive/{id} flip status=archived
//   POST   /api/admin/jev/templates/reload       re-scan FS + reload DB rows
//
// Observability endpoints (see also jev_observability.go):
//
//   GET /api/admin/jev/decisions   paginated decision log
//   GET /api/admin/jev/decisions/{id}
//   POST /api/admin/jev/decisions/{id}/review  mark accepted/rejected
//   GET /api/admin/jev/stats        rolled-up dashboard payload
//
// All routes are JWT-gated by the surrounding chi middleware.

package handler

import (
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"sync"

	"github.com/go-chi/chi/v5"
	"github.com/rs/zerolog"

	"intelligent_customer/backend/internal/apperr"
	"intelligent_customer/backend/internal/audit"
	"intelligent_customer/backend/internal/jev"
	"intelligent_customer/backend/internal/log"
	"intelligent_customer/backend/internal/tenant"
)

// JevAdmin groups all admin-side Jev endpoints behind one struct so
// main.go has a single place to wire dependencies.
type JevAdmin struct {
	Registry       *jev.Registry
	TemplateRepo   *jev.TemplateRepo
	DecisionRepo   *jev.DecisionRepo
	Loopback       jev.Loopback
	FSTemplatesDir string
	DB             *sql.DB
	Logger         zerolog.Logger

	// AuditSink 可选；template.publish / template.archive / template.delete / template.create。
	AuditSink audit.Emitter

	reloadMu sync.Mutex
}

// ListTemplates returns every jev_templates row for the request's
// tenant. The orchestrator falls back to "default" tenant templates
// when the per-tenant lookup misses; the admin UI wants both so we
// return the union here.
func (h *JevAdmin) ListTemplates(w http.ResponseWriter, r *http.Request) {
	lg := log.With(r.Context(), h.Logger).With().Str("endpoint", "jev_admin.list").Logger()
	t := tenant.FromContext(r.Context())
	rows, err := h.TemplateRepo.ListByTenant(r.Context(), t.ID)
	if err != nil {
		lg.Error().Err(err).Msg("jev_list_failed")
		writeAppError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"items": rows,
		"total": len(rows),
	})
}

// GetTemplate fetches a single template row by id.
func (h *JevAdmin) GetTemplate(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	rec, err := h.TemplateRepo.Get(r.Context(), id)
	if err != nil {
		writeAppError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, rec)
}

// CreateTemplate inserts a new template version. Caller is expected to
// supply tenant_id (defaults to ctx tenant) + name + version + trigger
// + output_type + labels + instructions + fallback. Status defaults to
// "draft"; admins use /publish to flip it.
func (h *JevAdmin) CreateTemplate(w http.ResponseWriter, r *http.Request) {
	lg := log.With(r.Context(), h.Logger).With().Str("endpoint", "jev_admin.create").Logger()
	var body jev.TemplateRecord
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<14)).Decode(&body); err != nil {
		writeAppError(w, r, apperr.BadRequest("invalid json body").WithCause(err))
		return
	}
	if body.TenantID == "" {
		body.TenantID = tenant.FromContext(r.Context()).ID
	}
	if body.Status == "" {
		body.Status = "draft"
	}
	if body.OutputType == "" {
		body.OutputType = jev.OutputChoice
	}
	if err := h.TemplateRepo.Create(r.Context(), &body); err != nil {
		lg.Warn().Err(err).Str("name", body.Name).Msg("jev_create_failed")
		writeAppError(w, r, err)
		return
	}
	if h.AuditSink != nil {
		h.AuditSink.EmitFromRequest(r, audit.ActionTemplateCreate, "template", body.ID,
			audit.Payload{"name": body.Name, "version": body.Version, "trigger": string(body.Trigger)})
	}
	writeJSON(w, http.StatusCreated, body)
}

// PublishTemplate flips a draft/approved row to published so the
// orchestrator picks it up.
func (h *JevAdmin) PublishTemplate(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if err := h.TemplateRepo.Publish(r.Context(), id); err != nil {
		writeAppError(w, r, err)
		return
	}
	// Reload registry so the change is live immediately.
	if err := h.Reload(r); err != nil {
		log.With(r.Context(), h.Logger).Warn().Err(err).Msg("jev_publish_reload_failed")
	}
	if h.AuditSink != nil {
		h.AuditSink.EmitFromRequest(r, audit.ActionTemplatePublish, "template", id, nil)
	}
	writeJSON(w, http.StatusOK, map[string]string{"id": id, "status": "published"})
}

// ArchiveTemplate marks the template archived; the registry reload
// will then stop exposing it.
func (h *JevAdmin) ArchiveTemplate(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if err := h.TemplateRepo.Archive(r.Context(), id); err != nil {
		writeAppError(w, r, err)
		return
	}
	if err := h.Reload(r); err != nil {
		log.With(r.Context(), h.Logger).Warn().Err(err).Msg("jev_archive_reload_failed")
	}
	if h.AuditSink != nil {
		h.AuditSink.EmitFromRequest(r, audit.ActionTemplateArchive, "template", id, nil)
	}
	writeJSON(w, http.StatusOK, map[string]string{"id": id, "status": "archived"})
}

// DeleteTemplate removes a template row outright (admin only — there
// is no undo). Reload ensures the registry drops it.
func (h *JevAdmin) DeleteTemplate(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if err := h.TemplateRepo.Delete(r.Context(), id); err != nil {
		writeAppError(w, r, err)
		return
	}
	if err := h.Reload(r); err != nil {
		log.With(r.Context(), h.Logger).Warn().Err(err).Msg("jev_delete_reload_failed")
	}
	if h.AuditSink != nil {
		h.AuditSink.EmitFromRequest(r, audit.ActionTemplateDelete, "template", id, nil)
	}
	w.WriteHeader(http.StatusNoContent)
}

// Reload re-scans FS templates + reloads DB templates into the in-memory
// registry. Returns the count of loaded templates per source.
func (h *JevAdmin) Reload(r *http.Request) error {
	h.reloadMu.Lock()
	defer h.reloadMu.Unlock()
	lg := log.With(r.Context(), h.Logger).With().Str("endpoint", "jev_admin.reload").Logger()

	fsLoaded, fsErrs := h.Registry.LoadFromFS(h.FSTemplatesDir)
	if len(fsErrs) > 0 {
		for _, e := range fsErrs {
			lg.Warn().Err(e).Msg("jev_reload_fs_error")
		}
	}
	if h.DB == nil {
		return apperr.Internal("jev admin: DB handle not configured")
	}
	dbLoaded, err := h.Registry.LoadFromDB(r.Context(), h.DB)
	if err != nil {
		lg.Error().Err(err).Msg("jev_reload_db_failed")
		return err
	}
	lg.Info().Int("fs", fsLoaded).Int("db", dbLoaded).Msg("jev_reload_done")
	return nil
}

// ReloadHTTP wraps Reload as an http.Handler.
func (h *JevAdmin) ReloadHTTP(w http.ResponseWriter, r *http.Request) {
	if err := h.Reload(r); err != nil {
		writeAppError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "reloaded"})
}

// ----- observability ---------------------------------------------------------

// ListDecisions returns paginated decision rows for the tenant.
func (h *JevAdmin) ListDecisions(w http.ResponseWriter, r *http.Request) {
	lg := log.With(r.Context(), h.Logger).With().Str("endpoint", "jev_admin.decisions").Logger()
	q := r.URL.Query()
	f := jev.DecisionFilter{
		TenantID:     tenant.FromContext(r.Context()).ID,
		TemplateName: q.Get("template"),
		Trigger:      jev.TriggerPoint(q.Get("trigger")),
		Status:       q.Get("status"),
	}
	f.Limit = parseIntDefault(q.Get("limit"), 50)
	f.Offset = parseIntDefault(q.Get("offset"), 0)
	rows, total, err := h.DecisionRepo.List(r.Context(), f)
	if err != nil {
		lg.Error().Err(err).Msg("jev_decisions_list_failed")
		writeAppError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"items":  rows,
		"total":  total,
		"limit":  f.Limit,
		"offset": f.Offset,
	})
}

// GetDecision returns a single decision row.
func (h *JevAdmin) GetDecision(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	rec, err := h.DecisionRepo.Get(r.Context(), id)
	if err != nil {
		writeAppError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, rec)
}

// ReviewDecision marks a decision as accepted/rejected with optional
// ground truth JSON. Used by admins from the observability page.
func (h *JevAdmin) ReviewDecision(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	var body struct {
		Status      string `json:"status"` // accepted | rejected
		GroundTruth string `json:"groundTruth"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<14)).Decode(&body); err != nil {
		writeAppError(w, r, apperr.BadRequest("invalid json body").WithCause(err))
		return
	}
	reviewer := usernameFromCtx(r.Context())
	if err := h.DecisionRepo.MarkReviewed(r.Context(), id, body.Status, body.GroundTruth, reviewer); err != nil {
		writeAppError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"id": id, "status": body.Status})
}

// Stats returns the rolled-up dashboard payload (decision counts,
// fallback ratio, P95 latency, per-template breakdown) for the
// trailing 7 days.
func (h *JevAdmin) Stats(w http.ResponseWriter, r *http.Request) {
	t := tenant.FromContext(r.Context())
	stats, err := h.DecisionRepo.Stats(r.Context(), t.ID, 0)
	if err != nil {
		writeAppError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, stats)
}

// ----- helpers ---------------------------------------------------------------

func writeJSON(w http.ResponseWriter, code int, body any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(body)
}

func parseIntDefault(s string, def int) int {
	if s == "" {
		return def
	}
	n, err := strconv.Atoi(s)
	if err != nil || n <= 0 {
		return def
	}
	return n
}

// usernameFromCtx extracts the JWT subject if present; falls back to
// "unknown" so reviewers always have a non-empty handle.
func usernameFromCtx(ctx context.Context) string {
	if v := ctx.Value("auth.username"); v != nil {
		if s, ok := v.(string); ok && s != "" {
			return s
		}
	}
	return "unknown"
}
