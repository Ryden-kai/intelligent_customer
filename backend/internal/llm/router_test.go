package llm

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rs/zerolog"
)

type fakeChannel struct {
	id      string
	chatFn  func(context.Context, string, []Message) (string, error)
	toolFn  func(context.Context, string, []Message, []ToolDef) (ToolChatResult, error)
	calls   int32
}

func (f *fakeChannel) Chat(ctx context.Context, sys string, msgs []Message) (string, error) {
	atomic.AddInt32(&f.calls, 1)
	if f.chatFn != nil {
		return f.chatFn(ctx, sys, msgs)
	}
	return "ok:" + f.id, nil
}

func (f *fakeChannel) ToolChat(ctx context.Context, sys string, msgs []Message, tools []ToolDef) (ToolChatResult, error) {
	atomic.AddInt32(&f.calls, 1)
	if f.toolFn != nil {
		return f.toolFn(ctx, sys, msgs, tools)
	}
	return ToolChatResult{Content: "ok:" + f.id}, nil
}

func (f *fakeChannel) Identity() Model {
	return Model{Provider: "fake", Name: f.id}
}

func (f *fakeChannel) Calls() int32 { return atomic.LoadInt32(&f.calls) }

func TestRouter_PicksPrimaryOnHappyPath(t *testing.T) {
	a := &fakeChannel{id: "openai"}
	b := &fakeChannel{id: "openrouter"}
	c := &fakeChannel{id: "minimax"}
	r := NewRouter([]ChannelConfig{
		{Slot: "primary", Provider: "openai", Channel: a, Regions: []string{"intl"}},
		{Slot: "secondary", Provider: "openrouter", Channel: b},
		{Slot: "tertiary", Provider: "minimax", Channel: c, Regions: []string{"cn"}},
	}, zerolog.Nop())

	out, err := r.Chat(context.Background(), "", nil)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if out != "ok:openai" {
		t.Fatalf("expected primary to win, got %q", out)
	}
	if a.Calls() != 1 || b.Calls() != 0 || c.Calls() != 0 {
		t.Fatalf("only primary should be touched: a=%d b=%d c=%d", a.Calls(), b.Calls(), c.Calls())
	}
}

func TestRouter_FallsBackToSecondaryOnPrimaryError(t *testing.T) {
	a := &fakeChannel{id: "openai", chatFn: func(context.Context, string, []Message) (string, error) {
		return "", errors.New("primary 502")
	}}
	b := &fakeChannel{id: "openrouter"}
	c := &fakeChannel{id: "minimax"}
	r := NewRouter([]ChannelConfig{
		{Slot: "primary", Provider: "openai", Channel: a, Regions: []string{"intl"}},
		{Slot: "secondary", Provider: "openrouter", Channel: b},
		{Slot: "tertiary", Provider: "minimax", Channel: c, Regions: []string{"cn"}},
	}, zerolog.Nop())

	out, err := r.Chat(context.Background(), "", nil)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if out != "ok:openrouter" {
		t.Fatalf("expected secondary to win after primary error, got %q", out)
	}
	if a.Calls() != 1 || b.Calls() != 1 || c.Calls() != 0 {
		t.Fatalf("primary+secondary only: a=%d b=%d c=%d", a.Calls(), b.Calls(), c.Calls())
	}
}

func TestRouter_AllChannelsFailReturnsApperr(t *testing.T) {
	mk := func(id string) *fakeChannel {
		return &fakeChannel{id: id, chatFn: func(context.Context, string, []Message) (string, error) {
			return "", errors.New(id + " down")
		}}
	}
	a, b, c := mk("a"), mk("b"), mk("c")
	r := NewRouter([]ChannelConfig{
		{Slot: "primary", Channel: a},
		{Slot: "secondary", Channel: b},
		{Slot: "tertiary", Channel: c},
	}, zerolog.Nop())

	_, err := r.Chat(context.Background(), "", nil)
	if err == nil {
		t.Fatal("expected err when every channel fails")
	}
	if !errors.Is(err, ErrAllChannelsFailed) {
		t.Fatalf("err chain missing ErrAllChannelsFailed: %v", err)
	}
}

func TestRouter_OpenBreakerSkipped(t *testing.T) {
	a := &fakeChannel{id: "openai", chatFn: func(context.Context, string, []Message) (string, error) {
		return "", errors.New("openai 500")
	}}
	b := &fakeChannel{id: "openrouter"}
	c := &fakeChannel{id: "minimax"}
	r := NewRouter([]ChannelConfig{
		{Slot: "primary", Channel: a},
		{Slot: "secondary", Channel: b},
		{Slot: "tertiary", Channel: c},
	}, zerolog.Nop())

	// Drive enough failures on primary to open the breaker.
	breakers := r.ChannelStates()
	if len(breakers) != 3 {
		t.Fatalf("expected 3 channels, got %d", len(breakers))
	}
	// Drive 3 failures through Chat to open primary's breaker.
	for i := 0; i < 3; i++ {
		_, _ = r.Chat(context.Background(), "", nil)
	}
	_ = breakers // referenced for the len assertion above
	out, err := r.Chat(context.Background(), "", nil)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if out != "ok:openrouter" {
		t.Fatalf("expected openrouter after primary opened, got %q", out)
	}
}

func TestRouter_RegionHintReorders(t *testing.T) {
	a := &fakeChannel{id: "openai"}
	b := &fakeChannel{id: "openrouter"}
	c := &fakeChannel{id: "minimax"}
	r := NewRouter([]ChannelConfig{
		{Slot: "primary", Channel: a, Regions: []string{"intl"}},
		{Slot: "secondary", Channel: b},
		{Slot: "tertiary", Channel: c, Regions: []string{"cn"}},
	}, zerolog.Nop())

	// cn tenant: minimax should be picked first.
	ctx := WithRegionHint(context.Background(), "cn")
	out, err := r.Chat(ctx, "", nil)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if out != "ok:minimax" {
		t.Fatalf("expected minimax first for cn region, got %q", out)
	}
	if c.Calls() != 1 || a.Calls() != 0 {
		t.Fatalf("minimax only: c=%d a=%d", c.Calls(), a.Calls())
	}
}

func TestRouter_CallbackReceivesAllAttempts(t *testing.T) {
	a := &fakeChannel{id: "openai", chatFn: func(context.Context, string, []Message) (string, error) {
		return "", errors.New("primary down")
	}}
	b := &fakeChannel{id: "openrouter"}
	r := NewRouter([]ChannelConfig{
		{Slot: "primary", Channel: a},
		{Slot: "secondary", Channel: b},
	}, zerolog.Nop())

	type evt struct {
		slot, provider string
		err            error
	}
	var got []evt
	r.RegisterCallback(func(slot, provider, model string, latency time.Duration, err error) {
		got = append(got, evt{slot, provider, err})
	})
	if _, err := r.Chat(context.Background(), "", nil); err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 callbacks, got %d", len(got))
	}
	if got[0].err == nil || got[1].err != nil {
		t.Fatalf("expected first callback err != nil, second err == nil; got %+v", got)
	}
}

func TestRouter_ToolChatPropagatesResult(t *testing.T) {
	a := &fakeChannel{id: "openai", toolFn: func(context.Context, string, []Message, []ToolDef) (ToolChatResult, error) {
		return ToolChatResult{Content: "tool-ok", Usage: TokenUsage{TotalTokens: 42}}, nil
	}}
	r := NewRouter([]ChannelConfig{
		{Slot: "primary", Channel: a},
	}, zerolog.Nop())
	res, err := r.ToolChat(context.Background(), "sys", nil, nil)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if res.Content != "tool-ok" || res.Usage.TotalTokens != 42 {
		t.Fatalf("tool result not propagated: %+v", res)
	}
}

func TestRouter_IdentityRespectsAliveChannel(t *testing.T) {
	a := &fakeChannel{id: "openai", chatFn: func(context.Context, string, []Message) (string, error) {
		return "", errors.New("x")
	}}
	b := &fakeChannel{id: "openrouter"}
	r := NewRouter([]ChannelConfig{
		{Slot: "primary", Channel: a},
		{Slot: "secondary", Channel: b},
	}, zerolog.Nop())

	if id := r.Identity().Name; id != "openai" {
		t.Fatalf("expected openai identity initially, got %q", id)
	}
	for i := 0; i < 5; i++ {
		_, _ = r.Chat(context.Background(), "", nil)
	}
	if id := r.Identity().Name; id != "openrouter" {
		t.Fatalf("expected openrouter identity after primary opened, got %q", id)
	}
}
