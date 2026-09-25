package handler

import (
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"

	"intelligent_customer/backend/internal/apperr"
	"intelligent_customer/backend/internal/audit"
)

// AdminAudit 暴露审计日志查询 / 详情 / 导出 3 个端点。
//
//   GET /api/admin/audit            list（4 维筛选 + 分页）
//   GET /api/admin/audit/{id}       详情
//   GET /api/admin/audit/export     CSV（UTF-8 BOM + RFC 4180）
type AdminAudit struct {
	Repo   *audit.Repo
	Logger interface{} // 防止缺依赖时编译失败；不强依赖
}

// parseTime 解析 RFC3339 / unix ms；空字符串返回零值。
func parseTime(s string) (time.Time, error) {
	if s == "" {
		return time.Time{}, nil
	}
	if n, err := strconv.ParseInt(s, 10, 64); err == nil {
		return time.UnixMilli(n), nil
	}
	return time.Parse(time.RFC3339, s)
}

// List GET /api/admin/audit
func (h *AdminAudit) List(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	from, _ := parseTime(q.Get("from"))
	to, _ := parseTime(q.Get("to"))
	if !to.IsZero() {
		// 让 to 包含当天：把 hh:mm:ss 推到当天最后一刻（用 999ms 即可）。
		to = time.UnixMilli(to.UnixMilli() + 24*60*60*1000 - 1)
	}
	limit, _ := strconv.Atoi(q.Get("limit"))
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	offset, _ := strconv.Atoi(q.Get("offset"))
	if offset < 0 {
		offset = 0
	}
	f := audit.Filter{
		From:       from,
		To:         to,
		ActorID:    q.Get("actor_id"),
		Action:     q.Get("action"),
		TargetType: q.Get("target_type"),
		TenantID:   q.Get("tenant_id"),
		Limit:      limit,
		Offset:     offset,
	}
	logs, total, err := h.Repo.List(r.Context(), f)
	if err != nil {
		writeAppError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"items":  logs,
		"total":  total,
		"limit":  limit,
		"offset": offset,
	})
}

// Get GET /api/admin/audit/{id}
func (h *AdminAudit) Get(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	lg, err := h.Repo.Get(r.Context(), id)
	if err != nil {
		if errors.Is(err, audit.ErrNotFound) {
			writeAppError(w, r, apperr.NotFound("audit log not found"))
			return
		}
		writeAppError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, lg)
}

// Export GET /api/admin/audit/export
//
// Content-Type: text/csv; charset=utf-8
// 第一行 BOM + RFC 4180。
func (h *AdminAudit) Export(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	from, _ := parseTime(q.Get("from"))
	to, _ := parseTime(q.Get("to"))
	if !to.IsZero() {
		to = time.UnixMilli(to.UnixMilli() + 24*60*60*1000 - 1)
	}
	f := audit.Filter{
		From:     from,
		To:       to,
		ActorID:  q.Get("actor_id"),
		Action:   q.Get("action"),
		TenantID: q.Get("tenant_id"),
	}
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="audit_logs.csv"`)
	if err := h.Repo.WriteCSVTo(r.Context(), f, w); err != nil {
		writeAppError(w, r, err)
		return
	}
}