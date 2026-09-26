// Package llm — router.go
//
// LLMRouter fans out chat / tool-chat calls across multiple LLM channels
// with per-channel circuit breakers. The agent loop and the public chat
// service see only the Router — they never know how many providers are
// wired in. v2.1 wiring (per docs/v2-llm-routing.md):
//
//	primary   = tenant.llm_primary   (default: openai)
//	secondary = tenant.llm_secondary (default: openrouter)
//	tertiary  = tenant.llm_tertiary  (default: minimax)
//
// Region hints:
//   - cn tenant → minimax preferred as primary (cheaper, in-region)
//   - intl tenant → openai / openrouter preferred
//
// On any non-recoverable error from a channel, the router marks that
// channel's breaker as failing and tries the next one. A channel whose
// breaker is OPEN is skipped entirely until the cooldown expires.

package llm

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/rs/zerolog"

	"intelligent_customer/backend/internal/apperr"
	"intelligent_customer/backend/internal/log"
)

// Channel is the minimal interface a Router can dispatch to. Both
// OpenAIClient and AnthropicClient satisfy it; tests plug a fake.
type Channel interface {
	Chat(ctx context.Context, systemPrompt string, msgs []Message) (string, error)
	ToolChat(ctx context.Context, systemPrompt string, msgs []Message, tools []ToolDef) (ToolChatResult, error)
	Identity() Model
}

// Router implements ChatCompleter + ToolChatCompleter by routing across
// N channels. It picks an order based on the tenant's region and slot
// preference, then walks channels until one succeeds or all fail.
type Router struct {
	mu        sync.RWMutex
	channels  []*routedChannel
	logger    zerolog.Logger
	callbacks []Callback
}

type routedChannel struct {
	slot     string // primary | secondary | tertiary
	provider string // openai | openrouter | minimax
	regions  []string
	channel  Channel
	breaker  *CircuitBreaker
}

// ChannelConfig is what callers pass to NewRouter. Regions is the set of
// tenant.regions this channel is the preferred primary for; an empty
// set means "applies to any region but only as fallback".
type ChannelConfig struct {
	Slot     string
	Provider string
	Regions  []string
	Channel  Channel
	Breaker  *CircuitBreaker // optional; nil gets a sensible default
}

// Callback lets observers (e.g. the llm_call_log repo) hook into every
// dispatch decision. Called AFTER the channel returns so the observer
// can record latency, fallback flag, error class, etc.
type Callback func(slot, provider, model string, latency time.Duration, err error)

// NewRouter wires the channels into a Router. Order in cfg defines the
// default preference; per-tenant routing is computed on top.
func NewRouter(cfg []ChannelConfig, logger zerolog.Logger) *Router {
	r := &Router{logger: logger}
	for _, c := range cfg {
		b := c.Breaker
		if b == nil {
			b = NewCircuitBreaker(CircuitBreakerConfig{
				FailureThreshold: 3,
				Cooldown:         30 * time.Second,
				OnStateChange:    r.logStateChange(c.Slot, c.Provider),
			})
		}
		r.channels = append(r.channels, &routedChannel{
			slot:     c.Slot,
			provider: c.Provider,
			regions:  c.Regions,
			channel:  c.Channel,
			breaker:  b,
		})
	}
	return r
}

// RegisterCallback lets wiring code plug a callback. Safe to call after
// the router is already serving — keeps tests simple.
func (r *Router) RegisterCallback(cb Callback) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.callbacks = append(r.callbacks, cb)
}

// ChannelCount returns the number of wired channels (exposed for tests
// + observability).
func (r *Router) ChannelCount() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.channels)
}

// ChannelStates returns a snapshot of breaker states, in slot order.
// Useful for /api/admin/llm/health-style endpoints.
func (r *Router) ChannelStates() []ChannelHealth {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]ChannelHealth, 0, len(r.channels))
	for _, c := range r.channels {
		st := c.breaker.State()
		out = append(out, ChannelHealth{
			Slot:     c.slot,
			Provider: c.provider,
			Model:    c.channel.Identity().Name,
			State:    st,
		})
	}
	return out
}

// ChannelHealth is one row of the ChannelStates snapshot.
type ChannelHealth struct {
	Slot     string       `json:"slot"`
	Provider string       `json:"provider"`
	Model    string       `json:"model"`
	State    CircuitState `json:"state"`
}

// Chat routes a plain text chat through the channels. Mirrors ChatCompleter.
func (r *Router) Chat(ctx context.Context, systemPrompt string, msgs []Message) (string, error) {
	return routeGeneric(ctx, r, func(ctx context.Context, ch Channel) (string, error) {
		return ch.Chat(ctx, systemPrompt, msgs)
	})
}

// ToolChat routes a tool-capable chat through the channels. Mirrors
// ToolChatCompleter so the existing agent loop keeps working unchanged.
func (r *Router) ToolChat(ctx context.Context, systemPrompt string, msgs []Message, tools []ToolDef) (ToolChatResult, error) {
	return routeGeneric(ctx, r, func(ctx context.Context, ch Channel) (ToolChatResult, error) {
		return ch.ToolChat(ctx, systemPrompt, msgs, tools)
	})
}

// Identity returns the active channel's identity. Useful for logging;
// the choice can flip on the next call.
func (r *Router) Identity() Model {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if len(r.channels) == 0 {
		return Model{Provider: "router", Name: "empty"}
	}
	for _, c := range r.channels {
		if c.breaker.Allow() {
			return c.channel.Identity()
		}
	}
	return r.channels[0].channel.Identity()
}

// order returns the channels to try for the given region, in preference
// order. Channels whose breaker is OPEN are dropped. Channels that
// match the region are preferred; configured slot ordering is the
// secondary key.
func (r *Router) order(region string) []*routedChannel {
	r.mu.RLock()
	defer r.mu.RUnlock()
	alive := make([]*routedChannel, 0, len(r.channels))
	for _, c := range r.channels {
		if c.breaker.Allow() {
			alive = append(alive, c)
		}
	}
	if region == "" {
		return alive
	}
	preferred := make([]*routedChannel, 0, len(alive))
	others := make([]*routedChannel, 0, len(alive))
	for _, c := range alive {
		if contains(c.regions, region) {
			preferred = append(preferred, c)
		} else {
			others = append(others, c)
		}
	}
	return append(preferred, others...)
}

// routeGeneric is the shared dispatch helper. It walks the channels in
// order, recording each attempt's outcome, until one succeeds or all
// fail. Generic over the return type so Chat and ToolChat share the loop.
func routeGeneric[T any](ctx context.Context, r *Router, call func(context.Context, Channel) (T, error)) (T, error) {
	var zero T
	region := regionFromCtx(ctx)
	channels := r.order(region)
	if len(channels) == 0 {
		return zero, apperr.Upstream("llm router: no healthy channels").WithCause(ErrAllChannelsFailed)
	}

	var lastErr error
	for i, c := range channels {
		start := time.Now()
		out, err := call(ctx, c.channel)
		latency := time.Since(start)
		r.recordOutcome(c, err, latency)
		if err == nil {
			return out, nil
		}
		lastErr = err
		if i < len(channels)-1 {
			lg := log.With(ctx, r.logger)
			lg.Warn().
				Err(err).
				Str("slot", c.slot).
				Str("provider", c.provider).
				Int64("latency_ms", latency.Milliseconds()).
				Msg("llm_router_channel_failed_falling_back")
		}
	}
	return zero, apperr.Upstream("llm router: all channels failed").WithCause(
		fmt.Errorf("%w (last channel: %w)", ErrAllChannelsFailed, lastErr),
	)
}

func (r *Router) recordOutcome(c *routedChannel, err error, latency time.Duration) {
	if err != nil {
		c.breaker.RecordFailure()
	} else {
		c.breaker.RecordSuccess()
	}
	r.mu.RLock()
	cbs := r.callbacks
	r.mu.RUnlock()
	for _, cb := range cbs {
		cb(c.slot, c.provider, c.channel.Identity().Name, latency, err)
	}
}

func (r *Router) logStateChange(slot, provider string) func(from, to CircuitState) {
	return func(from, to CircuitState) {
		r.logger.Warn().
			Str("slot", slot).
			Str("provider", provider).
			Str("from", string(from)).
			Str("to", string(to)).
			Msg("llm_router_circuit_state_change")
	}
}

func contains(xs []string, x string) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}

// regionFromCtx extracts the optional region hint set by the tenant
// middleware. Missing / empty → router treats the request as region-less
// and falls back to the configured slot ordering.
func regionFromCtx(ctx context.Context) string {
	if v, ok := ctx.Value(regionKey{}).(string); ok {
		return v
	}
	return ""
}

// RegionFromCtx is the exported variant of regionFromCtx — useful for
// integration tests and for any caller that wants to inspect the
// region hint set by the tenant middleware.
func RegionFromCtx(ctx context.Context) string { return regionFromCtx(ctx) }

// WithRegionHint attaches a region hint to ctx. The tenant middleware
// (cmd/server) calls this when it knows the tenant's region.
func WithRegionHint(ctx context.Context, region string) context.Context {
	return context.WithValue(ctx, regionKey{}, region)
}

type regionKey struct{}

// NewCallID returns a UUID — exposed so external callers (mainly the
// LLM call log repo) can generate ids for log rows without importing
// google/uuid themselves.
func NewCallID() string { return uuid.NewString() }

// ErrAllChannelsFailed signals that every configured channel returned an
// error. Surfaced via apperr so handlers can pick the right HTTP code.
var ErrAllChannelsFailed = errors.New("llm router: all channels failed")

// Compile-time checks: Router satisfies the public chat interfaces the
// agent loop + chat service depend on.
var (
	_ ChatCompleter     = (*Router)(nil)
	_ ToolChatCompleter = (*Router)(nil)
)
