// Dynamic skill loading: hot-load skill definitions from the filesystem
// (`data/skills/*.json`) and from the SQLite `skills` table. Both sources
// must declare ReadOnly=true — mutations stay in the built-in set.

package skill

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"intelligent_customer/backend/internal/apperr"
	"intelligent_customer/backend/internal/model"
)

// defaultHTTPClient is shared across dynamic http-echo skill executors.
// 10s timeout per call. agent layer wraps another WithTimeout on top.
var defaultHTTPClient = &http.Client{Timeout: 10 * time.Second}

// ----------------------------------------------------------------------------
// FS loader
// ----------------------------------------------------------------------------

// LoadFromFS scans dir for *.json files and registers each as a dynamic
// skill. The format mirrors the on-disk Skill struct exactly; any field
// missing falls back to zero-values. ReadOnly is forced to true regardless
// of the file content.
func (r *Registry) LoadFromFS(dir string) (int, error) {
	if dir == "" {
		return 0, nil
	}
	if _, err := os.Stat(dir); errors.Is(err, os.ErrNotExist) {
		return 0, nil // no dir → nothing to load, not an error
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0, fmt.Errorf("read skills dir %q: %w", dir, err)
	}
	count := 0
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		path := filepath.Join(dir, e.Name())
		raw, err := os.ReadFile(path)
		if err != nil {
			return count, fmt.Errorf("read %s: %w", path, err)
		}
		var s model.Skill
		if err := json.Unmarshal(raw, &s); err != nil {
			return count, fmt.Errorf("parse %s: %w", path, err)
		}
		exec, err := httpExecutorFromConfig(s.HandlerKind, s.HandlerConfig)
		if err != nil {
			return count, fmt.Errorf("handler in %s: %w", path, err)
		}
		if err := r.UpsertDynamic(Definition{
			Name:           s.Name,
			Description:    s.Description,
			Category:       s.Category,
			ParametersJSON: s.ParametersJSON,
			RequiresHuman:  false,
			ReadOnly:       true,
			Execute:        exec,
		}, "fs"); err != nil {
			return count, err
		}
		count++
	}
	return count, nil
}

// ----------------------------------------------------------------------------
// DB loader
// ----------------------------------------------------------------------------

// LoadFromDB reads enabled rows from the skills table and registers each
// as a dynamic skill. Called on startup and after admin CRUD.
func (r *Registry) LoadFromDB(ctx context.Context, db *sql.DB) (int, error) {
	if db == nil {
		return 0, nil
	}
	rows, err := db.QueryContext(ctx,
		`SELECT id, name, description, category, parameters_json, handler_kind, handler_config, enabled, requires_human, read_only
		 FROM skills WHERE enabled = 1 ORDER BY name ASC`)
	if err != nil {
		return 0, fmt.Errorf("query skills: %w", err)
	}
	defer rows.Close()
	count := 0
	for rows.Next() {
		var s model.Skill
		var enabled, reqHuman, readOnly int
		if err := rows.Scan(&s.ID, &s.Name, &s.Description, &s.Category, &s.ParametersJSON,
			&s.HandlerKind, &s.HandlerConfig, &enabled, &reqHuman, &readOnly); err != nil {
			return count, err
		}
		s.Enabled = enabled == 1
		s.RequiresHuman = reqHuman == 1
		s.ReadOnly = readOnly == 1
		exec, err := httpExecutorFromConfig(s.HandlerKind, s.HandlerConfig)
		if err != nil {
			return count, fmt.Errorf("handler for %s: %w", s.Name, err)
		}
		if err := r.UpsertDynamic(Definition{
			Name:           s.Name,
			Description:    s.Description,
			Category:       s.Category,
			ParametersJSON: s.ParametersJSON,
			RequiresHuman:  false,
			ReadOnly:       true,
			Execute:        exec,
		}, "db"); err != nil {
			return count, err
		}
		count++
	}
	return count, rows.Err()
}

// httpExecutorFromConfig builds an Execute func from the handler config.
// Only "http" is supported today; "echo" is a small dev-only handler used
// by the bundled sample file. Everything else returns an error.
func httpExecutorFromConfig(kind, cfgJSON string) (func(ctx context.Context, argsJSON string) (ExecutionResult, error), error) {
	switch kind {
	case "echo":
		// dev-only echo handler — handy for testing the pipeline without
		// standing up a real upstream.
		return func(ctx context.Context, argsJSON string) (ExecutionResult, error) {
			return ExecutionResult{Status: StatusOK, Data: map[string]any{
				"echo": argsJSON,
			}, Summary: "echo: " + truncate(argsJSON, 80)}, nil
		}, nil
	case "http":
		var cfg struct {
			URL    string `json:"url"`
			Method string `json:"method"`
		}
		if err := json.Unmarshal([]byte(cfgJSON), &cfg); err != nil {
			return nil, fmt.Errorf("parse handler_config: %w", err)
		}
		if cfg.URL == "" {
			return nil, errors.New("handler_config.url required")
		}
		if cfg.Method == "" {
			cfg.Method = "POST"
		}
		return httpCallExecutor(cfg.URL, cfg.Method), nil
	default:
		return nil, fmt.Errorf("unknown handler_kind %q (want http | echo)", kind)
	}
}

// httpCallExecutor POSTs the args JSON to the configured URL and returns
// the response body as Data. It has a hard 5s timeout enforced at agent
// layer; this executor is read-only and never mutates upstream state.
func httpCallExecutor(url, method string) func(ctx context.Context, argsJSON string) (ExecutionResult, error) {
	return func(ctx context.Context, argsJSON string) (ExecutionResult, error) {
		cctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		req, err := http.NewRequestWithContext(cctx, method, url, strings.NewReader(argsJSON))
		if err != nil {
			return ExecutionResult{}, err
		}
		if argsJSON != "" {
			req.Header.Set("Content-Type", "application/json")
		}
		resp, err := defaultHTTPClient.Do(req)
		if err != nil {
			return ExecutionResult{}, apperr.Upstream(fmt.Sprintf("skill http: %v", err)).WithCause(err)
		}
		defer resp.Body.Close()
		body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
		if err != nil {
			return ExecutionResult{}, err
		}
		var data map[string]any
		if len(body) > 0 && body[0] == '{' {
			_ = json.Unmarshal(body, &data)
		}
		if data == nil {
			data = map[string]any{"raw": string(body)}
		}
		return ExecutionResult{
			Status:  StatusOK,
			Data:    data,
			Summary: fmt.Sprintf("%s %s → %d", method, url, resp.StatusCode),
		}, nil
	}
}

// ----------------------------------------------------------------------------
// Skill summary helper for the admin list endpoint.
// ----------------------------------------------------------------------------

// SnapshotMeta returns metadata for every skill, including disabled ones.
// Used by the admin endpoint so the UI can render an enable toggle.
func (r *Registry) SnapshotMeta() []SkillMeta {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]SkillMeta, 0, len(r.defs))
	for n, d := range r.defs {
		out = append(out, SkillMeta{
			Name:          n,
			Description:   d.Description,
			Category:      d.Category,
			Enabled:       d.Enabled,
			RequiresHuman: d.RequiresHuman,
			ReadOnly:      d.ReadOnly,
			Source:        r.source[n],
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// SkillMeta is the slim view used by /api/admin/skills.
type SkillMeta struct {
	Name          string `json:"name"`
	Description   string `json:"description"`
	Category      string `json:"category"`
	Enabled       bool   `json:"enabled"`
	RequiresHuman bool   `json:"requiresHuman"`
	ReadOnly      bool   `json:"readOnly"`
	Source        string `json:"source"`
}