package tenant

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/google/uuid"
)

// Tenant is the persisted shape of a tenant row (extends Info with
// config + LLM routing hints + timestamps).
type Tenant struct {
	ID           string    `json:"id"`
	Name         string    `json:"name"`
	Region       string    `json:"region"` // cn | intl
	Status       string    `json:"status"` // active | suspended
	LLMPrimary   string    `json:"llmPrimary"`
	LLMSecondary string    `json:"llmSecondary"`
	LLMTertiary  string    `json:"llmTertiary"`
	ConfigJSON   string    `json:"configJson"`
	CreatedAt    time.Time `json:"createdAt"`
	UpdatedAt    time.Time `json:"updatedAt"`
}

// ToInfo projects the persisted tenant into the lightweight Info carried
// in ctx (no DB / no config).
func (t Tenant) ToInfo() Info {
	return Info{
		ID:        t.ID,
		Name:      t.Name,
		Region:    t.Region,
		IsDefault: t.ID == DefaultID,
	}
}

// Repo provides CRUD over the tenants table. All callers go through here;
// the rest of the codebase never writes SQL for tenants directly.
type Repo struct {
	DB *sql.DB
}

func NewRepo(db *sql.DB) *Repo { return &Repo{DB: db} }

// ErrNotFound is returned when a tenant lookup misses. Mirrors the
// admin_users convention so callers can branch with errors.Is.
var ErrNotFound = errors.New("tenant not found")

// Get loads a tenant by id. Returns ErrNotFound on miss.
func (r *Repo) Get(ctx context.Context, id string) (*Tenant, error) {
	row := r.DB.QueryRowContext(ctx,
		`SELECT id, name, region, status, llm_primary, llm_secondary, llm_tertiary,
		        config_json, created_at, updated_at
		 FROM tenants WHERE id=?`, id)
	return scanTenant(row)
}

// EnsureDefault inserts the tnt_default row if it doesn't exist. Idempotent
// — useful for test fixtures and for the seed.Run startup path. Returns
// the resulting Tenant (whether newly inserted or pre-existing).
func (r *Repo) EnsureDefault(ctx context.Context) (*Tenant, error) {
	if t, err := r.Get(ctx, DefaultID); err == nil {
		return t, nil
	} else if !errors.Is(err, ErrNotFound) {
		return nil, err
	}
	now := time.Now().UnixMilli()
	t := &Tenant{
		ID:           DefaultID,
		Name:         "Default Tenant",
		Region:       "cn",
		Status:       "active",
		LLMPrimary:   "openai",
		LLMSecondary: "openrouter",
		LLMTertiary:  "minimax",
		ConfigJSON:   "{}",
		CreatedAt:    time.UnixMilli(now),
		UpdatedAt:    time.UnixMilli(now),
	}
	_, err := r.DB.ExecContext(ctx,
		`INSERT INTO tenants(id, name, region, status, llm_primary, llm_secondary,
		                    llm_tertiary, config_json, created_at, updated_at)
		 VALUES(?,?,?,?,?,?,?,?,?,?)`,
		t.ID, t.Name, t.Region, t.Status, t.LLMPrimary, t.LLMSecondary,
		t.LLMTertiary, t.ConfigJSON, now, now)
	if err != nil {
		return nil, err
	}
	return t, nil
}

// List returns every tenant, ordered by id. Bounded — we don't expect
// more than a handful per server.
func (r *Repo) List(ctx context.Context) ([]Tenant, error) {
	rows, err := r.DB.QueryContext(ctx,
		`SELECT id, name, region, status, llm_primary, llm_secondary, llm_tertiary,
		        config_json, created_at, updated_at
		 FROM tenants ORDER BY id ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]Tenant, 0, 4)
	for rows.Next() {
		t, err := scanTenant(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *t)
	}
	return out, rows.Err()
}

// Upsert creates or updates a tenant. Used by the admin endpoint and by
// the seed path. The config_json is overwritten verbatim — callers must
// encode any structured config themselves.
func (r *Repo) Upsert(ctx context.Context, t *Tenant) error {
	if t.ID == "" {
		t.ID = "tnt_" + uuid.NewString()[:8]
	}
	now := time.Now().UnixMilli()
	if t.CreatedAt.IsZero() {
		t.CreatedAt = time.UnixMilli(now)
	}
	t.UpdatedAt = time.UnixMilli(now)
	_, err := r.DB.ExecContext(ctx,
		`INSERT INTO tenants(id, name, region, status, llm_primary, llm_secondary,
		                    llm_tertiary, config_json, created_at, updated_at)
		 VALUES(?,?,?,?,?,?,?,?,?,?)
		 ON CONFLICT(id) DO UPDATE SET
		   name=excluded.name,
		   region=excluded.region,
		   status=excluded.status,
		   llm_primary=excluded.llm_primary,
		   llm_secondary=excluded.llm_secondary,
		   llm_tertiary=excluded.llm_tertiary,
		   config_json=excluded.config_json,
		   updated_at=excluded.updated_at`,
		t.ID, t.Name, t.Region, t.Status, t.LLMPrimary, t.LLMSecondary,
		t.LLMTertiary, t.ConfigJSON, t.CreatedAt.UnixMilli(), now)
	return err
}

// Delete removes a tenant. Refuses to delete tnt_default (would orphan
// v1 data). Returns ErrNotFound when the row didn't exist.
func (r *Repo) Delete(ctx context.Context, id string) error {
	if id == DefaultID {
		return errors.New("tenant: cannot delete default tenant")
	}
	res, err := r.DB.ExecContext(ctx, `DELETE FROM tenants WHERE id=?`, id)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// scanTenant reads one tenant row regardless of source (QueryRow or
// QueryContext — both expose Scan).
func scanTenant(s interface {
	Scan(dest ...any) error
}) (*Tenant, error) {
	var (
		t         Tenant
		createdAt int64
		updatedAt int64
	)
	if err := s.Scan(&t.ID, &t.Name, &t.Region, &t.Status,
		&t.LLMPrimary, &t.LLMSecondary, &t.LLMTertiary,
		&t.ConfigJSON, &createdAt, &updatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	t.CreatedAt = time.UnixMilli(createdAt)
	t.UpdatedAt = time.UnixMilli(updatedAt)
	return &t, nil
}
