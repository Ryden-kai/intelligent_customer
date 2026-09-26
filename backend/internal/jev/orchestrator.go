// Package jev — orchestrator.go
//
// Orchestrator owns the decision-point lifecycle for v2.1:
//
//	caller → Orchestrator.Decide(req)
//	            ├── Registry.Get(tenant, trigger)        (resolve template)
//	            ├── Client.Decide(template, input)      (live call, may fail)
//	            ├── Fallback.Handle(err)                 (local rules / static default)
//	            ├── DecisionRepo.Insert(...)             (audit + loopback input)
//	            └── Loopback.Record(...)                 (no-op for in-mem backend)
//
// The orchestrator is the only place that knows how a Template maps to
// a Jev API call — handlers / services always go through Decide().
//
// Metrics surface area:
//   - per-template success / fallback counts (derived from DecisionRepo.Stats)
//   - latency P95 (same)
//   - cost (each Decide stamps CostUSD — wired to OpenRouter pricing later)
//
// Failure semantics:
//   - Live call fails AND fallback handler returns ok       → Decision{Fallback=true, ...}
//   - Live call fails AND fallback handler returns error    → Decide returns error;
//                                                              caller may retry or
//                                                              bubble up to a
//                                                              handover.
//   - Template missing → fallback handler called with empty
//     template; static default in the template spec wins.

package jev

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/rs/zerolog"

	"intelligent_customer/backend/internal/apperr"
	"intelligent_customer/backend/internal/log"
	"intelligent_customer/backend/internal/tenant"
)

// DecisionRequest is the public input to Orchestrator.Decide. It is
// intentionally trigger-shaped so callers can't accidentally mix slots.
type DecisionRequest struct {
	TenantID  string
	Trigger   TriggerPoint
	Input     map[string]any // template variables (e.g. {"message":"hi"})
	TraceID   string
	Timeout   time.Duration
}

// Decision is the public output. Choice + Scores are the Jev-side
// fields; Fallback indicates whether the static / local-rule path was
// taken so observability can count it.
type Decision struct {
	TenantID        string         `json:"tenantId"`
	TemplateName    string         `json:"templateName"`
	TemplateVersion int            `json:"templateVersion"`
	Trigger         TriggerPoint   `json:"trigger"`
	OutputType      OutputType     `json:"outputType"`
	Choice          string         `json:"choice,omitempty"`
	Scores          map[string]any `json:"scores,omitempty"`
	Fallback        bool           `json:"fallback"`
	FallbackReason  string         `json:"fallbackReason,omitempty"`
	Confidence      *float64       `json:"confidence,omitempty"`
	LatencyMS       int            `json:"latencyMs"`
	CostUSD         float64        `json:"costUsd"`
	TraceID         string         `json:"traceId"`
	DecisionID      string         `json:"decisionId"`
}

// Fallback is the contract for the offline decision path. Implementations
// must be deterministic + cheap (P95 ≤ 10 ms).
type Fallback interface {
	Handle(ctx context.Context, tpl *Template, req DecisionRequest, liveErr error) (*Decision, error)
}

// Loopback is the contract for feeding decisions back into the audit
// table. The interface accepts the live Decision so the implementer can
// include context fields (ground truth source, etc.) without coupling
// to the orchestrator's metrics.
type Loopback interface {
	Record(ctx context.Context, d *Decision) error
}

// LiveClient is the minimal contract Orchestrator needs from jev.Client.
// jev.go's *Client satisfies it directly; tests can plug a fake.
type LiveClient interface {
	Decide(ctx context.Context, tpl *Template, req DecisionRequest) (*Decision, error)
	Enabled() bool
}

// Metrics is the side-channel for observability. The Orchestrator emits
// one event per call so callers can wire Prometheus / OTEL / zerolog
// without coupling to a specific backend.
type Metrics interface {
	ObserveDecide(d *Decision, liveErr error)
}

// nullMetrics is the default no-op sink; callers pass a real impl if
// they want Prometheus-style metrics.
type nullMetrics struct{}

func (nullMetrics) ObserveDecide(*Decision, error) {}

// Orchestrator wires everything together. Constructed once at startup
// and shared across handlers (no per-request state).
type Orchestrator struct {
	registry *Registry
	client   LiveClient
	fallback Fallback
	loopback Loopback
	repo     *DecisionRepo
	metrics  Metrics
	logger   zerolog.Logger
	defaultTimeout time.Duration
}

// Options groups Orchestrator construction knobs. Zero-value Metrics
// is OK — we substitute nullMetrics internally.
type Options struct {
	Registry       *Registry
	Client         LiveClient
	Fallback       Fallback
	Loopback       Loopback
	Repo           *DecisionRepo
	Metrics        Metrics
	Logger         zerolog.Logger
	DefaultTimeout time.Duration
}

// NewOrchestrator builds an Orchestrator. Required fields: Registry,
// Client, Fallback. Repo + Loopback + Metrics are optional — missing
// ones degrade gracefully (no audit, no loopback, no metrics).
func NewOrchestrator(opts Options) *Orchestrator {
	if opts.Metrics == nil {
		opts.Metrics = nullMetrics{}
	}
	if opts.DefaultTimeout == 0 {
		opts.DefaultTimeout = 1500 * time.Millisecond
	}
	return &Orchestrator{
		registry:       opts.Registry,
		client:         opts.Client,
		fallback:       opts.Fallback,
		loopback:       opts.Loopback,
		repo:           opts.Repo,
		metrics:        opts.Metrics,
		logger:         opts.Logger,
		defaultTimeout: opts.DefaultTimeout,
	}
}

// Decide resolves the template, calls the live client, falls back on
// failure, writes the audit row, and returns the resulting Decision.
// The whole flow is bounded by req.Timeout (falls back to opts.DefaultTimeout).
func (o *Orchestrator) Decide(ctx context.Context, req DecisionRequest) (*Decision, error) {
	lg := log.With(ctx, o.logger).With().
		Str("trigger", string(req.Trigger)).
		Str("tenant", req.TenantID).
		Logger()

	timeout := req.Timeout
	if timeout == 0 {
		timeout = o.defaultTimeout
	}
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	tpl, terr := o.registry.Get(req.TenantID, req.Trigger)
	if terr != nil {
		// Missing template: try fallback (it has a built-in default
		// per trigger). If fallback also fails, return NotFound.
		lg.Warn().Err(terr).Msg("jev_template_missing_using_fallback")
		d, ferr := o.fallback.Handle(cctx, nil, req, terr)
		if ferr != nil {
			return nil, apperr.NotFound(fmt.Sprintf("no template and no fallback for trigger %s", req.Trigger))
		}
		o.finalize(cctx, d, req, "template_missing", nil)
		return d, nil
	}

	// Live call. Respect client.Enabled() so disabled Jev configs
	// never even hit the network.
	if !o.client.Enabled() {
		d, ferr := o.fallback.Handle(cctx, tpl, req, errClientDisabled)
		if ferr != nil {
			return nil, apperr.Upstream("jev disabled and no fallback").WithCause(ferr)
		}
		o.finalize(cctx, d, req, "client_disabled", errClientDisabled)
		return d, nil
	}

	start := time.Now()
	live, lerr := o.client.Decide(cctx, tpl, req)
	latency := time.Since(start)

	if lerr == nil {
		live.DecisionID = uuid.NewString()
		live.TemplateName = tpl.Name
		live.TemplateVersion = tpl.Version
		live.TenantID = req.TenantID
		live.Trigger = req.Trigger
		live.OutputType = tpl.OutputType
		live.LatencyMS = int(latency.Milliseconds())
		live.TraceID = req.TraceID
		o.metrics.ObserveDecide(live, nil)
		o.persist(cctx, live)
		return live, nil
	}

	// Live failed — try fallback.
	lg.Warn().Err(lerr).Str("template", tpl.Name).Msg("jev_live_call_failed_using_fallback")
	d, ferr := o.fallback.Handle(cctx, tpl, req, lerr)
	if ferr != nil {
		// Surface the live error so the caller can decide; metrics
		// still see the failure.
		o.metrics.ObserveDecide(nil, lerr)
		return nil, apperr.Upstream("jev: live and fallback both failed").WithCause(lerr)
	}
	o.finalize(cctx, d, req, "live_call_failed", lerr)
	return d, nil
}

// errClientDisabled is the sentinel error passed to Fallback.Handle when
// the live client is disabled. Implementations can branch on it.
var errClientDisabled = errors.New("jev client disabled")

// finalize stamps the common audit fields on a fallback Decision and
// pushes it through persist + metrics.
func (o *Orchestrator) finalize(ctx context.Context, d *Decision, req DecisionRequest, reason string, liveErr error) {
	d.DecisionID = uuid.NewString()
	if d.TemplateName == "" {
		d.TemplateName = string(req.Trigger) // fallback path: we may not know the name
	}
	d.TenantID = req.TenantID
	d.Trigger = req.Trigger
	d.Fallback = true
	d.FallbackReason = reason
	d.TraceID = req.TraceID
	o.metrics.ObserveDecide(d, liveErr)
	o.persist(ctx, d)
}

// persist writes the decision row (if a repo is wired) AND invokes
// the loopback sink (if wired). The two are independent — callers can
// enable loopback without DB persistence (e.g. in-memory tests) and
// vice versa.
func (o *Orchestrator) persist(ctx context.Context, d *Decision) {
	if o.repo != nil {
		inputHash := hashDecisionInput(d.Trigger, d)
		rec := &DecisionRecord{
			ID:              d.DecisionID,
			TenantID:        d.TenantID,
			TemplateName:    d.TemplateName,
			TemplateVersion: d.TemplateVersion,
			Trigger:         d.Trigger,
			InputHash:       inputHash,
			InputJSON:       marshalForLog(d),
			OutputJSON:      outputJSONFor(d),
			Fallback:        d.Fallback,
			Confidence:      d.Confidence,
			LatencyMS:       d.LatencyMS,
			CostUSD:         d.CostUSD,
			Status:          "decided",
			TraceID:         d.TraceID,
		}
		if err := o.repo.Insert(ctx, rec); err != nil {
			log.With(ctx, o.logger).Warn().Err(err).Msg("jev_persist_failed")
		}
	}
	if o.loopback != nil {
		if err := o.loopback.Record(ctx, d); err != nil {
			log.With(ctx, o.logger).Warn().Err(err).Msg("jev_loopback_record_failed")
		}
	}
}

// ----- helpers ---------------------------------------------------------------

func hashDecisionInput(_ TriggerPoint, d *Decision) string {
	payload := struct {
		Choice string         `json:"c"`
		Scores map[string]any `json:"s"`
	}{d.Choice, d.Scores}
	b, _ := json.Marshal(payload)
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func marshalForLog(d *Decision) string {
	b, err := json.Marshal(d)
	if err != nil {
		return "{}"
	}
	return string(b)
}

func outputJSONFor(d *Decision) string {
	out := map[string]any{
		"choice":        d.Choice,
		"scores":        d.Scores,
		"outputType":    d.OutputType,
		"fallback":      d.Fallback,
		"fallbackReason": d.FallbackReason,
		"confidence":    d.Confidence,
		"latencyMs":     d.LatencyMS,
		"costUsd":       d.CostUSD,
	}
	b, _ := json.Marshal(out)
	return string(b)
}

// DecideFromContext is a convenience wrapper that pulls the tenant
// from ctx via the tenant package and stamps the request trace id.
func (o *Orchestrator) DecideFromContext(ctx context.Context, trigger TriggerPoint, input map[string]any) (*Decision, error) {
	t := tenant.FromContext(ctx)
	return o.Decide(ctx, DecisionRequest{
		TenantID: t.ID,
		Trigger:  trigger,
		Input:    input,
		TraceID:  log.RequestIDFrom(ctx),
	})
}

// ErrNoFallback is returned by Fallback implementations when they
// have no rule for the trigger. Wrap it in AppError via WithCause to
// let the orchestrator bubble up cleanly.
var ErrNoFallback = errors.New("jev fallback: no rule for trigger")
