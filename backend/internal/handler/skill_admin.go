// Skill admin endpoints + the public refund-confirm endpoint. Admin
// routes require JWT; refund-confirm only needs the ticket id + the
// conversation's user id (so it's bound to the conversation owner).

package handler

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"sync"

	"github.com/rs/zerolog"

	"intelligent_customer/backend/internal/apperr"
	"intelligent_customer/backend/internal/audit"
	"intelligent_customer/backend/internal/log"
	"intelligent_customer/backend/internal/model"
	"intelligent_customer/backend/internal/skill"
)

type SkillAdmin struct {
	Repo        *skill.Skills
	Registry    *skill.Registry
	Invocations *skill.Invocations
	Tickets     *skill.Tickets
	FSDir       string
	Logger      zerolog.Logger

	// ReloadMu serialises concurrent reload triggers; the underlying
	// registry is itself goroutine-safe so this is just for log shape.
	ReloadMu sync.Mutex

	// AuditSink 可选；skills.create / skills.toggle / skills.delete 埋点。
	AuditSink audit.Emitter
}

// List returns every skill (enabled + disabled) so the admin UI can
// render an enable toggle.
func (h *SkillAdmin) List(w http.ResponseWriter, r *http.Request) {
	lg := log.With(r.Context(), h.Logger).With().Str("endpoint", "skill_admin.list").Logger()
	items, err := h.Repo.List(r.Context())
	if err != nil {
		lg.Error().Err(err).Msg("skill_admin_list_failed")
		writeAppError(w, r, err)
		return
	}
	meta := h.Registry.SnapshotMeta()
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"items": items,
		"meta":  meta,
		"total": len(items),
	})
}

// Create inserts a new dynamic skill. Returns 400 if validation fails.
func (h *SkillAdmin) Create(w http.ResponseWriter, r *http.Request) {
	lg := log.With(r.Context(), h.Logger).With().Str("endpoint", "skill_admin.create").Logger()
	var body model.Skill
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<14)).Decode(&body); err != nil {
		lg.Warn().Err(err).Msg("skill_admin_create_decode_failed")
		writeAppError(w, r, apperr.BadRequest("invalid json body").WithCause(err))
		return
	}
	if err := h.Repo.Create(r.Context(), &body); err != nil {
		lg.Error().Err(err).Msg("skill_admin_create_failed")
		writeAppError(w, r, err)
		return
	}
	if err := h.reload(r.Context()); err != nil {
		lg.Warn().Err(err).Msg("skill_admin_reload_failed_after_create")
	}
	if h.AuditSink != nil {
		h.AuditSink.EmitFromRequest(r, audit.ActionSkillCreate, "skill", body.ID,
			audit.Payload{"name": body.Name, "category": body.Category})
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(body)
}

// Update changes description / category / parameters / handler config /
// enabled flag. Name is immutable (audit rows reference it).
func (h *SkillAdmin) Update(w http.ResponseWriter, r *http.Request) {
	lg := log.With(r.Context(), h.Logger).With().Str("endpoint", "skill_admin.update").Logger()
	id := pathID(r.URL.Path, "/api/admin/skills/")
	if id == "" {
		writeAppError(w, r, apperr.BadRequest("missing skill id"))
		return
	}
	var body model.Skill
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<14)).Decode(&body); err != nil {
		lg.Warn().Err(err).Msg("skill_admin_update_decode_failed")
		writeAppError(w, r, apperr.BadRequest("invalid json body").WithCause(err))
		return
	}
	body.ID = id
	if err := h.Repo.Update(r.Context(), &body); err != nil {
		if errors.Is(err, skill.ErrNotFound) {
			writeAppError(w, r, apperr.NotFound("skill not found"))
			return
		}
		lg.Error().Err(err).Msg("skill_admin_update_failed")
		writeAppError(w, r, err)
		return
	}
	if err := h.reload(r.Context()); err != nil {
		lg.Warn().Err(err).Msg("skill_admin_reload_failed_after_update")
	}
	if h.AuditSink != nil {
		h.AuditSink.EmitFromRequest(r, audit.ActionSkillToggle, "skill", id,
			audit.Payload{"enabled": body.Enabled})
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(body)
}

// Delete removes a dynamic skill by id.
func (h *SkillAdmin) Delete(w http.ResponseWriter, r *http.Request) {
	lg := log.With(r.Context(), h.Logger).With().Str("endpoint", "skill_admin.delete").Logger()
	id := pathID(r.URL.Path, "/api/admin/skills/")
	if id == "" {
		writeAppError(w, r, apperr.BadRequest("missing skill id"))
		return
	}
	if err := h.Repo.Delete(r.Context(), id); err != nil {
		if errors.Is(err, skill.ErrNotFound) {
			writeAppError(w, r, apperr.NotFound("skill not found"))
			return
		}
		lg.Error().Err(err).Msg("skill_admin_delete_failed")
		writeAppError(w, r, err)
		return
	}
	if err := h.reload(r.Context()); err != nil {
		lg.Warn().Err(err).Msg("skill_admin_reload_failed_after_delete")
	}
	if h.AuditSink != nil {
		h.AuditSink.EmitFromRequest(r, audit.ActionSkillDelete, "skill", id, nil)
	}
	w.WriteHeader(http.StatusNoContent)
}

// Reload forces a re-read of dynamic skills from the DB. Useful after
// bulk imports or recovery from a failed config push.
func (h *SkillAdmin) Reload(w http.ResponseWriter, r *http.Request) {
	if err := h.reload(r.Context()); err != nil {
		writeAppError(w, r, err)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"reloaded": h.Registry.Names(),
	})
}

// reload is shared by every mutating admin handler. It clears the
// dynamic skills and reloads from BOTH the filesystem and the DB so
// the in-memory registry reflects the latest state.
func (h *SkillAdmin) reload(ctx context.Context) error {
	h.ReloadMu.Lock()
	defer h.ReloadMu.Unlock()
	_, _, err := h.Registry.ReloadAll(ctx, h.Repo.DB, h.FSDir)
	return err
}

// pathID extracts the trailing id from a request URL.
func pathID(path, prefix string) string {
	if len(path) <= len(prefix) {
		return ""
	}
	return path[len(prefix):]
}

// ----------------------------------------------------------------------------
// Public refund-confirm endpoint (no JWT — auth is the ticket+user id
// pair).
// ----------------------------------------------------------------------------

type RefundConfirm struct {
	Tickets *skill.Tickets
	Logger  zerolog.Logger
}

type confirmReq struct {
	TicketID string `json:"ticketId"`
	UserID   string `json:"userId"`
}

// Confirm applies the pending mutation after the user has clicked
// "确认" in the UI. The user_id must match the ticket owner.
func (h *RefundConfirm) Confirm(w http.ResponseWriter, r *http.Request) {
	lg := log.With(r.Context(), h.Logger).With().Str("endpoint", "refund.confirm").Logger()
	var body confirmReq
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<14)).Decode(&body); err != nil {
		writeAppError(w, r, apperr.BadRequest("invalid json body").WithCause(err))
		return
	}
	if body.TicketID == "" || body.UserID == "" {
		writeAppError(w, r, apperr.BadRequest("ticketId and userId required"))
		return
	}
	t, err := h.Tickets.Confirm(r.Context(), body.TicketID, body.UserID)
	if err != nil {
		// Treat ErrNotFound as a 403 instead of leaking "the ticket does
		// not exist" — keeps "no such ticket" indistinguishable from
		// "wrong user", which avoids giving an attacker a per-ticket
		// existence oracle.
		if errors.Is(err, skill.ErrNotFound) {
			lg.Warn().Str("ticket_id", body.TicketID).Msg("refund_confirm_not_found")
			writeAppError(w, r, apperr.Forbidden("ticket not found or not yours"))
			return
		}
		var ae *apperr.AppError
		if errors.As(err, &ae) {
			lg.Warn().Err(err).Str("code", string(ae.Code)).Int("http", ae.HTTPStatus).Msg("refund_confirm_rejected")
		} else {
			lg.Error().Err(err).Msg("refund_confirm_failed")
		}
		writeAppError(w, r, err)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(t)
}

type cancelReq struct {
	TicketID string `json:"ticketId"`
	UserID   string `json:"userId"`
}

// Cancel marks a pending ticket as cancelled. Same auth model as
// Confirm: ticket id + user id must match.
func (h *RefundConfirm) Cancel(w http.ResponseWriter, r *http.Request) {
	lg := log.With(r.Context(), h.Logger).With().Str("endpoint", "refund.cancel").Logger()
	var body cancelReq
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<14)).Decode(&body); err != nil {
		writeAppError(w, r, apperr.BadRequest("invalid json body").WithCause(err))
		return
	}
	if err := h.Tickets.Cancel(r.Context(), body.TicketID, body.UserID); err != nil {
		if errors.Is(err, skill.ErrNotFound) {
			writeAppError(w, r, apperr.NotFound("ticket not found or already finalised"))
			return
		}
		lg.Error().Err(err).Msg("refund_cancel_failed")
		writeAppError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}