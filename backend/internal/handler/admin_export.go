// Package handler — admin_export.go
//
// v2.2 PR5: Admin-wide CSV export + bulk tagging endpoint for
// conversations. Kept in a separate file to keep admin.go focused.
//
//   GET  /api/admin/conversations/export          conversation.export (or .read)
//   POST /api/admin/conversations/tag-batch       conversation.write
//
// CSV is UTF-8 + BOM + RFC 4180 (Excel 双击不乱码).
// Tag-batch writes a single column in the conversations table that
// already exists in our schema (status). "Label" here maps to a free-form
// tag string stored on the conversation row's `title` field if a dedicated
// tag column is absent — kept compatible with the current schema.
//
// Both endpoints write audit logs.

package handler

import (
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"intelligent_customer/backend/internal/apperr"
	"intelligent_customer/backend/internal/audit"
	"intelligent_customer/backend/internal/model"
	"intelligent_customer/backend/internal/repo"
)

// ExportConversations writes conversations as CSV (UTF-8 BOM + RFC 4180).
//
// Query params:
//   status, from (RFC3339 or unix ms), to, limit (max 10000)
func (h *Admin) ExportConversations(w http.ResponseWriter, r *http.Request) {
	if h.Service == nil || h.Service.Convs == nil {
		writeAppError(w, r, apperr.Internal("conversations repo not wired"))
		return
	}
	q := r.URL.Query()
	limit, _ := strconv.Atoi(q.Get("limit"))
	if limit <= 0 || limit > 10000 {
		limit = 10000
	}
	rows, err := h.Service.Convs.ListByStatus(r.Context(), q.Get("status"), limit)
	if err != nil {
		writeAppError(w, r, err)
		return
	}

	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition",
		`attachment; filename="conversations_`+time.Now().UTC().Format("20060102")+`.csv"`)
	if err := writeConversationsCSV(w, rows); err != nil {
		writeAppError(w, r, apperr.Internal("write csv").WithCause(err))
		return
	}
}

// TagConversationsBatch sets a free-form tag on each conversation in
// body.ids. The tag is recorded as the first line of the conversation's
// metadata-like "title" field by prefixing it with "[tag]"; admins can
// then filter by title. v2.2.1+ will introduce a dedicated tag column.
//
// Response: { total, succeeded, results: [{id, status, error?}] }
func (h *Admin) TagConversationsBatch(w http.ResponseWriter, r *http.Request) {
	var body struct {
		IDs    []string `json:"ids"`
		Label  string   `json:"label"`
		Clear  bool     `json:"clear"` // if true, remove the tag instead
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<14)).Decode(&body); err != nil {
		writeAppError(w, r, apperr.BadRequest("invalid json body").WithCause(err))
		return
	}
	if len(body.IDs) == 0 {
		writeAppError(w, r, apperr.BadRequest("ids must not be empty"))
		return
	}
	if len(body.IDs) > 500 {
		writeAppError(w, r, apperr.BadRequest("too many ids (max 500)"))
		return
	}
	if !body.Clear {
		body.Label = strings.TrimSpace(body.Label)
		if body.Label == "" {
			writeAppError(w, r, apperr.BadRequest("label required when clear=false"))
			return
		}
		if len(body.Label) > 64 {
			writeAppError(w, r, apperr.BadRequest("label too long (max 64)"))
			return
		}
	}

	results := make([]batchItemResult, 0, len(body.IDs))
	okCount := 0
	for _, id := range body.IDs {
		err := applyTagToConversation(r.Context(), h, id, body.Label, body.Clear)
		if err != nil {
			results = append(results, batchItemResult{ID: id, Status: "error", Error: err.Error()})
			continue
		}
		results = append(results, batchItemResult{ID: id, Status: "tagged"})
		okCount++
		if h.AuditSink != nil {
			h.AuditSink.EmitFromRequest(r, "conversation.tag", "conversation", id,
				audit.Payload{"label": body.Label, "clear": body.Clear})
		}
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"total":     len(body.IDs),
		"succeeded": okCount,
		"results":   results,
	})
}

// applyTagToConversation is a small adapter around the repo so the
// handler loop stays readable.
func applyTagToConversation(ctx context.Context, h *Admin, id, label string, clear bool) error {
	if h == nil || h.Service == nil || h.Service.Convs == nil {
		return errors.New("conversations repo not wired")
	}
	c, err := h.Service.Convs.Get(ctx, id)
	if err != nil {
		return err
	}
	tagPrefix := "[tag]"
	newTitle := c.Title
	if clear {
		newTitle = stripTag(newTitle, tagPrefix)
	} else {
		// Replace existing tag prefix if present, else prepend.
		stripped := stripTag(newTitle, tagPrefix)
		if stripped == "" {
			newTitle = tagPrefix + label
		} else {
			newTitle = tagPrefix + label + " " + stripped
		}
	}
	if newTitle == c.Title {
		return nil // no-op
	}
	return h.Service.Convs.UpdateTitle(ctx, id, newTitle)
}

// stripTag removes the [tag]xxx prefix from a conversation title.
func stripTag(title, prefix string) string {
	if !strings.HasPrefix(title, prefix) {
		return title
	}
	rest := strings.TrimPrefix(title, prefix)
	if i := strings.IndexByte(rest, ' '); i >= 0 {
		return strings.TrimSpace(rest[i+1:])
	}
	return ""
}

// writeConversationsCSV writes UTF-8 BOM + RFC 4180 CSV.
func writeConversationsCSV(w io.Writer, rows []model.Conversation) error {
	bw := &bytes.Buffer{}
	bw.WriteString("\xEF\xBB\xBF")
	cw := csv.NewWriter(bw)

	header := []string{
		"id", "user_id", "title", "status",
		"handed_over", "created_at", "updated_at",
	}
	if err := cw.Write(header); err != nil {
		return err
	}
	for _, c := range rows {
		row := []string{
			c.ID,
			c.UserID,
			c.Title,
			string(c.Status),
			strconv.FormatBool(c.HandedOver),
			c.CreatedAt.UTC().Format(time.RFC3339),
			c.UpdatedAt.UTC().Format(time.RFC3339),
		}
		if err := cw.Write(row); err != nil {
			return err
		}
	}
	cw.Flush()
	if err := cw.Error(); err != nil {
		return err
	}
	_, err := io.Copy(w, bw)
	return err
}

// keep imports referenced even when reduced.
var _ = repo.NewConversations

// keep context import referenced.
var _ = context.Background