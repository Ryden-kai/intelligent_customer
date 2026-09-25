package ratelimit

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// Repo rate_limit_configs 仓储。
type Repo struct {
	DB *sql.DB
}

// NewRepo 构造。
func NewRepo(db *sql.DB) *Repo { return &Repo{DB: db} }

// ErrNotFound 找不到配置。
var ErrNotFound = errors.New("ratelimit: config not found")

// ListAll 列出全部 rate_limit_configs（含 disabled）。
func (r *Repo) ListAll(ctx context.Context, tenantID string) ([]Config, error) {
	var (
		rows *sql.Rows
		err  error
	)
	if tenantID == "" {
		rows, err = r.DB.QueryContext(ctx,
			`SELECT id, tenant_id, endpoint, dimension, per_minute, per_hour, burst, enabled, description
			 FROM rate_limit_configs ORDER BY endpoint, dimension`)
	} else {
		rows, err = r.DB.QueryContext(ctx,
			`SELECT id, tenant_id, endpoint, dimension, per_minute, per_hour, burst, enabled, description
			 FROM rate_limit_configs WHERE tenant_id = ? ORDER BY endpoint, dimension`, tenantID)
	}
	if err != nil {
		return nil, fmt.Errorf("ratelimit: list: %w", err)
	}
	defer rows.Close()
	out := []Config{}
	for rows.Next() {
		var c Config
		var enabled int
		if err := rows.Scan(&c.ID, &c.TenantID, &c.Endpoint, &c.Dimension,
			&c.PerMinute, &c.PerHour, &c.Burst, &enabled, &c.Description); err != nil {
			return nil, fmt.Errorf("ratelimit: scan: %w", err)
		}
		c.Enabled = enabled == 1
		out = append(out, c)
	}
	return out, rows.Err()
}

// ListEnabled 仅返回 enabled=1 的配置，供 LimiterMap 启动时加载。
func (r *Repo) ListEnabled(ctx context.Context) ([]Config, error) {
	all, err := r.ListAll(ctx, "")
	if err != nil {
		return nil, err
	}
	out := make([]Config, 0, len(all))
	for _, c := range all {
		if c.Enabled {
			out = append(out, c)
		}
	}
	return out, nil
}

// GetByID 按 ID 取单条。
func (r *Repo) GetByID(ctx context.Context, id string) (*Config, error) {
	var c Config
	var enabled int
	err := r.DB.QueryRowContext(ctx,
		`SELECT id, tenant_id, endpoint, dimension, per_minute, per_hour, burst, enabled, description
		 FROM rate_limit_configs WHERE id = ?`, id,
	).Scan(&c.ID, &c.TenantID, &c.Endpoint, &c.Dimension,
		&c.PerMinute, &c.PerHour, &c.Burst, &enabled, &c.Description)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("ratelimit: get: %w", err)
	}
	c.Enabled = enabled == 1
	return &c, nil
}

// Update 修改限流配置。
//
// 仅可改 per_minute / per_hour / burst / enabled / description；
// endpoint / dimension / tenant_id 不允许改（防越权把 admin write 改成 chat）。
func (r *Repo) Update(ctx context.Context, id string, c Config) (*Config, error) {
	existing, err := r.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if c.PerMinute > 0 {
		existing.PerMinute = c.PerMinute
	}
	if c.PerHour > 0 {
		existing.PerHour = c.PerHour
	}
	if c.Burst > 0 {
		existing.Burst = c.Burst
	}
	existing.Enabled = c.Enabled
	if c.Description != "" {
		existing.Description = c.Description
	}
	_, err = r.DB.ExecContext(ctx,
		`UPDATE rate_limit_configs SET per_minute=?, per_hour=?, burst=?, enabled=?, description=?, updated_at=? WHERE id=?`,
		existing.PerMinute, existing.PerHour, existing.Burst, boolInt(existing.Enabled), existing.Description,
		time.Now().UnixMilli(), id)
	if err != nil {
		return nil, fmt.Errorf("ratelimit: update: %w", err)
	}
	return existing, nil
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}