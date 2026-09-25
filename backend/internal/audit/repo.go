package audit

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
)

// AuditLog 是从 audit_logs 表读出的行；与 logger.Event 字段一致但用 string 时间戳。
type AuditLog struct {
	ID         string    `json:"id"`
	TenantID   string    `json:"tenant_id"`
	Timestamp  time.Time `json:"timestamp"`
	ActorID    string    `json:"actor_id"`
	ActorEmail string    `json:"actor_email"`
	Action     string    `json:"action"`
	TargetType string    `json:"target_type,omitempty"`
	TargetID   string    `json:"target_id,omitempty"`
	IP         string    `json:"ip,omitempty"`
	UserAgent  string    `json:"user_agent,omitempty"`
	Payload    Payload   `json:"payload_json,omitempty"`
}

// Filter 是审计日志列表的查询条件。
//
// 全部字段可选；零值表示不过滤。
type Filter struct {
	From       time.Time // 含；零值 = 不过滤
	To         time.Time // 含；零值 = 不过滤
	ActorID    string
	Action     string
	TargetType string
	TenantID   string
	Limit      int
	Offset     int
}

// Repo 是 audit_logs 的仓储。
type Repo struct {
	DB *sql.DB
}

// NewRepo 构造 Repo。
func NewRepo(db *sql.DB) *Repo { return &Repo{DB: db} }

// ErrNotFound 找不到单条记录时返回。
var ErrNotFound = errors.New("audit: log not found")

// InsertBatch 一次性插入多条事件。在事务内执行，保证原子性。
//
// payload 字段序列化为 JSON；空 payload 写 "{}" 而非 NULL。
func (r *Repo) InsertBatch(ctx context.Context, events []Event) error {
	if len(events) == 0 {
		return nil
	}
	tx, err := r.DB.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("audit: begin tx: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck

	stmt, err := tx.PrepareContext(ctx,
		`INSERT INTO audit_logs(id, tenant_id, timestamp, actor_id, actor_email, action, target_type, target_id, ip, user_agent, payload_json, created_at)
		 VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		return fmt.Errorf("audit: prepare: %w", err)
	}
	defer stmt.Close()

	for _, ev := range events {
		payloadBytes := []byte("{}")
		if len(ev.Payload) > 0 {
			b, err := json.Marshal(ev.Payload)
			if err != nil {
				return fmt.Errorf("audit: marshal payload: %w", err)
			}
			payloadBytes = b
		}
		_, err := stmt.ExecContext(ctx,
			ev.ID,
			ev.TenantID,
			ev.Timestamp.UnixMilli(),
			ev.ActorID,
			ev.ActorEmail,
			ev.Action,
			ev.TargetType,
			ev.TargetID,
			ev.IP,
			ev.UserAgent,
			string(payloadBytes),
			time.Now().UnixMilli(),
		)
		if err != nil {
			return fmt.Errorf("audit: exec: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("audit: commit: %w", err)
	}
	return nil
}

// List 按 filter 条件查询审计日志（分页 + 倒序时间）。
func (r *Repo) List(ctx context.Context, f Filter) ([]AuditLog, int, error) {
	var (
		conds []string
		args  []any
	)
	if !f.From.IsZero() {
		conds = append(conds, "timestamp >= ?")
		args = append(args, f.From.UnixMilli())
	}
	if !f.To.IsZero() {
		conds = append(conds, "timestamp <= ?")
		args = append(args, f.To.UnixMilli())
	}
	if f.ActorID != "" {
		conds = append(conds, "actor_id = ?")
		args = append(args, f.ActorID)
	}
	if f.Action != "" {
		conds = append(conds, "action = ?")
		args = append(args, f.Action)
	}
	if f.TargetType != "" {
		conds = append(conds, "target_type = ?")
		args = append(args, f.TargetType)
	}
	if f.TenantID != "" {
		conds = append(conds, "tenant_id = ?")
		args = append(args, f.TenantID)
	}
	where := ""
	if len(conds) > 0 {
		where = " WHERE " + strings.Join(conds, " AND ")
	}

	// count
	var total int
	if err := r.DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM audit_logs"+where, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("audit: count: %w", err)
	}

	limit := f.Limit
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	offset := f.Offset
	if offset < 0 {
		offset = 0
	}

	rows, err := r.DB.QueryContext(ctx,
		"SELECT id, tenant_id, timestamp, actor_id, actor_email, action, target_type, target_id, ip, user_agent, payload_json FROM audit_logs"+where+
			" ORDER BY timestamp DESC LIMIT ? OFFSET ?",
		append(args, limit, offset)...)
	if err != nil {
		return nil, 0, fmt.Errorf("audit: list: %w", err)
	}
	defer rows.Close()

	out := make([]AuditLog, 0, limit)
	for rows.Next() {
		var (
			row         AuditLog
			tsMs        int64
			payloadJSON sql.NullString
		)
		if err := rows.Scan(&row.ID, &row.TenantID, &tsMs, &row.ActorID, &row.ActorEmail,
			&row.Action, &row.TargetType, &row.TargetID, &row.IP, &row.UserAgent, &payloadJSON); err != nil {
			return nil, 0, fmt.Errorf("audit: scan: %w", err)
		}
		row.Timestamp = time.UnixMilli(tsMs)
		if payloadJSON.Valid && payloadJSON.String != "" {
			_ = json.Unmarshal([]byte(payloadJSON.String), &row.Payload)
		}
		out = append(out, row)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("audit: iterate: %w", err)
	}
	return out, total, nil
}

// Get 按 ID 取单条审计日志；找不到返回 ErrNotFound。
func (r *Repo) Get(ctx context.Context, id string) (*AuditLog, error) {
	var (
		row         AuditLog
		tsMs        int64
		payloadJSON sql.NullString
	)
	err := r.DB.QueryRowContext(ctx,
		`SELECT id, tenant_id, timestamp, actor_id, actor_email, action, target_type, target_id, ip, user_agent, payload_json
		 FROM audit_logs WHERE id = ?`, id,
	).Scan(&row.ID, &row.TenantID, &tsMs, &row.ActorID, &row.ActorEmail,
		&row.Action, &row.TargetType, &row.TargetID, &row.IP, &row.UserAgent, &payloadJSON)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("audit: get: %w", err)
	}
	row.Timestamp = time.UnixMilli(tsMs)
	if payloadJSON.Valid && payloadJSON.String != "" {
		_ = json.Unmarshal([]byte(payloadJSON.String), &row.Payload)
	}
	return &row, nil
}

// ExportCSV 把查询结果导出为 CSV。
//
// 输出格式：
//   - 首行 UTF-8 BOM（\xEF\xBB\xBF）→ Excel 双击不乱码
//   - 列顺序固定：id, timestamp, actor_id, actor_email, action, target_type, target_id, ip, user_agent, payload_json
//   - 字段含逗号 / 引号 / 换行按 RFC 4180 转义
//
// 与 List 共用筛选条件；返回 *bytes.Buffer（已包含 BOM）。
func (r *Repo) ExportCSV(ctx context.Context, f Filter) (*bytes.Buffer, error) {
	// 导出时禁用 limit / offset，由流式 cursor 自己决定。
	f2 := f
	f2.Limit = 10000
	f2.Offset = 0

	logs, _, err := r.List(ctx, f2)
	if err != nil {
		return nil, err
	}
	buf := &bytes.Buffer{}
	// UTF-8 BOM。
	buf.WriteString("\xEF\xBB\xBF")

	w := csv.NewWriter(buf)
	defer w.Flush()

	header := []string{"id", "timestamp", "actor_id", "actor_email", "action",
		"target_type", "target_id", "ip", "user_agent", "payload_json"}
	if err := w.Write(header); err != nil {
		return nil, err
	}
	for _, lg := range logs {
		row := []string{
			lg.ID,
			lg.Timestamp.UTC().Format(time.RFC3339),
			lg.ActorID,
			lg.ActorEmail,
			lg.Action,
			lg.TargetType,
			lg.TargetID,
			lg.IP,
			lg.UserAgent,
			flattenPayload(lg.Payload),
		}
		if err := w.Write(row); err != nil {
			return nil, err
		}
	}
	return buf, nil
}

// flattenPayload 把 Payload 序列化为单行字符串（JSON 压缩）。
func flattenPayload(p Payload) string {
	if len(p) == 0 {
		return ""
	}
	b, err := json.Marshal(p)
	if err != nil {
		return ""
	}
	return string(b)
}

// CountByAction 统计每种 action 的条数（管理后台"按动作类型"饼图用）。
type ActionCount struct {
	Action string `json:"action"`
	Count  int    `json:"count"`
}

func (r *Repo) CountByAction(ctx context.Context, tenantID string, since time.Time) ([]ActionCount, error) {
	conds := []string{}
	args := []any{}
	if tenantID != "" {
		conds = append(conds, "tenant_id = ?")
		args = append(args, tenantID)
	}
	if !since.IsZero() {
		conds = append(conds, "timestamp >= ?")
		args = append(args, since.UnixMilli())
	}
	where := ""
	if len(conds) > 0 {
		where = " WHERE " + strings.Join(conds, " AND ")
	}
	rows, err := r.DB.QueryContext(ctx,
		"SELECT action, COUNT(*) FROM audit_logs"+where+" GROUP BY action ORDER BY COUNT(*) DESC", args...)
	if err != nil {
		return nil, fmt.Errorf("audit: count by action: %w", err)
	}
	defer rows.Close()
	out := []ActionCount{}
	for rows.Next() {
		var ac ActionCount
		if err := rows.Scan(&ac.Action, &ac.Count); err != nil {
			return nil, err
		}
		out = append(out, ac)
	}
	return out, rows.Err()
}

// DistinctActors 返回 audit_logs 中出现过的 actor_id 集合（去重）。
func (r *Repo) DistinctActors(ctx context.Context, tenantID string) ([]string, error) {
	q := `SELECT DISTINCT actor_id FROM audit_logs WHERE actor_id != ''`
	args := []any{}
	if tenantID != "" {
		q += ` AND tenant_id = ?`
		args = append(args, tenantID)
	}
	q += ` ORDER BY actor_id LIMIT 200`
	rows, err := r.DB.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// WriteCSVTo 是 ExportCSV 的便捷包装：直接写到 io.Writer（http.ResponseWriter 等）。
func (r *Repo) WriteCSVTo(ctx context.Context, f Filter, w io.Writer) error {
	buf, err := r.ExportCSV(ctx, f)
	if err != nil {
		return err
	}
	_, err = io.Copy(w, buf)
	return err
}

// ensure uuid import referenced even when not strictly used
var _ = uuid.NewString

// avoid unused import in some go versions
var _ = strconv.Itoa