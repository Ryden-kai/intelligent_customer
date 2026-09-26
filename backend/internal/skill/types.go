// Package skill is the registry + executors for tool-style skills that
// the Agent exposes to the LLM. Three source kinds are merged into one
// registry at startup:
//
//   1. Built-in: hard-coded Go functions in this package (most reliable)
//   2. Dynamic FS: data/skills/*.json, hot-loaded on file change (flexible)
//   3. Dynamic DB: SQLite `skills` table, edited via admin CRUD (auditable)
//
// Dynamic skills are forced to be read-only at load time. Mutations
// (refund, cancel, password reset) must live in the built-in set so a
// rogue admin row can't trigger a transfer.
package skill

import (
	"context"
	"database/sql"
	"errors"
	"sync"
	"time"
)

// Definition is the in-memory shape of a skill. It carries everything the
// LLM needs (Name + Description + Parameters schema) plus an Executable
// the agent calls when the LLM decides to use it.
type Definition struct {
	Name           string
	Description    string
	Category       string
	ParametersJSON string // JSON Schema for tool arguments
	Enabled        bool   // disabled skills stay in registry but are hidden from the LLM
	RequiresHuman  bool   // if true, ExecutionResult.Status = "pending_human"
	ReadOnly       bool   // if false, dynamic loader refuses to register this skill
	Execute        func(ctx context.Context, argsJSON string) (ExecutionResult, error)
}

// ExecutionResult is what a skill returns. Two non-success states the
// agent cares about:
//
//	StatusPendingHumanOK - mutation queued; user must call /confirm
//	StatusTimeout       - skill ran too long; agent gives up this turn
//
// All other failures are returned as the second return value (Go error);
// the agent wraps them and continues the loop.
type ExecutionResult struct {
	Status        string         `json:"status"`         // ok | pending_human | timeout
	Data          map[string]any `json:"data,omitempty"` // free-form payload returned to LLM
	Summary       string         `json:"summary"`        // human-readable line for logs
	PendingTicket string         `json:"pendingTicket,omitempty"`
}

// Execution status constants. Anything else in Status is treated as "ok".
const (
	StatusOK            = "ok"
	StatusPendingHuman  = "pending_human"
	StatusTimeout       = "timeout"
)

// ErrSkillNotFound is returned by Registry.Get when the name is unknown.
var ErrSkillNotFound = errors.New("skill not found")

// ----------------------------------------------------------------------------
// Registry: thread-safe map from skill name to Definition.
// ----------------------------------------------------------------------------

// Registry aggregates all skill sources. New() initialises with built-ins;
// LoadFromFS and LoadFromDB add dynamic ones. Disabled skills are kept in
// the map but excluded from the LLM-facing snapshot.
type Registry struct {
	mu     sync.RWMutex
	defs   map[string]*Definition
	source map[string]string // "builtin" | "fs" | "db"
}

// NewRegistry returns an empty registry; call Register / Load* to populate.
func NewRegistry() *Registry {
	return &Registry{
		defs:   make(map[string]*Definition),
		source: make(map[string]string),
	}
}

// Register adds a built-in skill. Re-registering the same name is an
// error so the wire-up step fails loud (no silent override).
func (r *Registry) Register(d Definition) error {
	if d.Name == "" {
		return errors.New("skill name required")
	}
	if d.Execute == nil {
		return errors.New("skill Execute func required")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.defs[d.Name]; exists {
		return errors.New("skill already registered: " + d.Name)
	}
	cp := d
	r.defs[d.Name] = &cp
	r.source[d.Name] = "builtin"
	return nil
}

// UpsertDynamic adds or replaces a dynamic skill. It rejects
// non-read-only skills (C6) so a dynamic row can never trigger state
// changes.
func (r *Registry) UpsertDynamic(d Definition, source string) error {
	if d.Name == "" {
		return errors.New("skill name required")
	}
	if !d.ReadOnly {
		return errors.New("dynamic skill must be read_only (mutations are builtin-only)")
	}
	if d.RequiresHuman {
		return errors.New("dynamic skill cannot require human confirmation")
	}
	if d.Execute == nil {
		return errors.New("skill Execute func required")
	}
	// Dynamic skills default to enabled — the loaders always pass
	// d.Enabled=true for fresh rows; disabling is a separate explicit
	// call on the registry.
	if !d.Enabled {
		d.Enabled = true
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	cp := d
	r.defs[d.Name] = &cp
	if source == "" {
		source = "db"
	}
	r.source[d.Name] = source
	return nil
}

// Disable marks a dynamic skill as no longer exposed to the LLM. Built-in
// skills cannot be disabled (they are part of the binary).
func (r *Registry) Disable(name string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	d, ok := r.defs[name]
	if !ok {
		return ErrSkillNotFound
	}
	if r.source[name] == "builtin" {
		return errors.New("cannot disable built-in skill: " + name)
	}
	d.Enabled = false
	return nil
}

// Enable re-exposes a previously disabled dynamic skill.
func (r *Registry) Enable(name string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	d, ok := r.defs[name]
	if !ok {
		return ErrSkillNotFound
	}
	d.Enabled = true
	return nil
}

// Get returns the definition by name. Returns ErrSkillNotFound if missing
// or disabled.
func (r *Registry) Get(name string) (*Definition, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	d, ok := r.defs[name]
	if !ok {
		return nil, ErrSkillNotFound
	}
	if !d.Enabled {
		return nil, ErrSkillNotFound
	}
	return d, nil
}

// Snapshot returns a copy of all enabled definitions, in stable order
// (sorted by name). The agent passes this list to the LLM as tools.
func (r *Registry) Snapshot() []Definition {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]Definition, 0, len(r.defs))
	for _, d := range r.defs {
		if !d.Enabled {
			continue
		}
		out = append(out, *d)
	}
	// Sort by name so the tool list is deterministic across calls.
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j-1].Name > out[j].Name; j-- {
			out[j-1], out[j] = out[j], out[j-1]
		}
	}
	return out
}

// Names returns all enabled skill names. Useful for log lines.
func (r *Registry) Names() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]string, 0, len(r.defs))
	for n, d := range r.defs {
		if d.Enabled {
			out = append(out, n)
		}
	}
	return out
}

// Source returns "builtin" | "fs" | "db" for a name, or "" if unknown.
func (r *Registry) Source(name string) string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.source[name]
}

// Reload clears all non-builtin entries. The caller re-loads dynamic
// sources after calling this. Built-ins are preserved.
func (r *Registry) Reload() {
	r.mu.Lock()
	defer r.mu.Unlock()
	for n, src := range r.source {
		if src == "builtin" {
			continue
		}
		delete(r.defs, n)
		delete(r.source, n)
	}
}

// ReloadAll clears all dynamic entries and reloads from BOTH fs and DB.
// Convenience for the admin "reload" endpoint so the caller doesn't
// have to remember the two-pass dance. Returns (fsCount, dbCount, err).
func (r *Registry) ReloadAll(ctx context.Context, db *sql.DB, fsDir string) (int, int, error) {
	r.Reload()
	fsCount, err := r.LoadFromFS(fsDir)
	if err != nil {
		return fsCount, 0, err
	}
	dbCount, err := r.LoadFromDB(ctx, db)
	if err != nil {
		return fsCount, dbCount, err
	}
	return fsCount, dbCount, nil
}

// ----------------------------------------------------------------------------
// Tiny helper: withTimeout executes the skill with a budget. The agent
// passes a per-skill timeout (default 5s) so one slow tool can't stall the
// loop.
// ----------------------------------------------------------------------------

func ExecuteWithTimeout(ctx context.Context, d *Definition, argsJSON string, budget time.Duration) (ExecutionResult, error) {
	if budget <= 0 {
		budget = 5 * time.Second
	}
	cctx, cancel := context.WithTimeout(ctx, budget)
	defer cancel()
	type out struct {
		r ExecutionResult
		e error
	}
	done := make(chan out, 1)
	go func() {
		r, e := d.Execute(cctx, argsJSON)
		done <- out{r, e}
	}()
	select {
	case <-cctx.Done():
		// If the goroutine finished at the same time we report ok; otherwise
		// it's a timeout.
		select {
		case o := <-done:
			return o.r, o.e
		default:
			return ExecutionResult{Status: StatusTimeout, Summary: "skill timed out"}, nil
		}
	case o := <-done:
		return o.r, o.e
	}
}