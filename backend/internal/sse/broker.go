// Package sse implements a minimal Server-Sent Events broker for the
// admin realtime refresh feature (v2.2 PR5).
//
// Design notes (docs/v2-architecture-design.md §3.3.2 + §5.4):
//
//   - per-tenant pub/sub: each tenant has its own topic (key = tenantID).
//     When a tenant has no subscribers the topic is dropped.
//   - subscribe / publish are non-blocking: Publish drops to a slow client
//     after 64 buffered events and logs a warning. Subscribe never blocks
//     the publisher.
//   - no server-side heartbeat (PRD Q-C): EventSource auto-reconnects
//     when the proxy drops the connection. The broker still detects
//     half-closed clients on the next Publish attempt and prunes them.
//   - topics are protected by RWMutex; subscribers are protected by a
//     per-subscriber channel mutex.
package sse

import (
	"sync"
	"time"
)

// Event is the wire payload for one SSE message.
//
// Type is one of "stats_update", "jev_decision", "audit_new"
// (reserved for v2.2.1). Payload is a JSON blob (string-encoded to
// keep SSE framing simple — EventSource will receive it as-is).
type Event struct {
	Type    string `json:"type"`
	Payload string `json:"payload"`
}

// NewEvent constructs an Event with a JSON-encoded payload if non-empty.
func NewEvent(typ, payload string) Event {
	return Event{Type: typ, Payload: payload}
}

// Subscriber represents one connected client.
//
// Buffered channel gives the publisher a non-blocking target; when the
// buffer fills we drop the client (slow consumer detection). The
// Done channel is closed when the handler wants the broker to stop
// sending events (typically when the HTTP response is closed by the
// client).
type Subscriber struct {
	id   string
	ch   chan Event
	done chan struct{}
	once sync.Once
}

// ID returns the subscriber's stable identifier (used in debug logs).
func (s *Subscriber) ID() string { return s.id }

// Events returns the receive-only channel for incoming events.
func (s *Subscriber) Events() <-chan Event { return s.ch }

// Done returns a channel closed by the broker when the subscriber is
// no longer being fed (handler may use it to abort server-side work).
func (s *Subscriber) Done() <-chan struct{} { return s.done }

// Close marks the subscriber as done; idempotent.
func (s *Subscriber) Close() {
	s.once.Do(func() { close(s.done) })
}

// bufferSize is the per-subscriber channel buffer. 64 events @ ~1 KB
// each ≈ 64 KB per client — modest for a backend with few simultaneous
// admin tabs.
const bufferSize = 64

// Broker fans events out to subscribers grouped by tenant.
//
// Zero value is ready to use (sync.RWMutex is the only field that
// requires zero-value validity; the maps are nil-safe via direct
// assignment).
type Broker struct {
	mu          sync.RWMutex
	subscribers map[string]map[*Subscriber]struct{} // tenantID -> subscribers
	nextID      uint64
}

// NewBroker constructs an empty broker.
func NewBroker() *Broker {
	return &Broker{
		subscribers: make(map[string]map[*Subscriber]struct{}),
	}
}

// Subscribe registers a new subscriber for tenantID and returns it.
// The caller MUST call Close when finished (handler defers it on
// request context cancellation).
//
// If tenantID is empty the subscriber is bucketed under "" (the
// broadcast tenant). This is convenient for the unauthenticated
// global health stream; PR5 only uses the default tenant path
// (admin pages are always tenant-scoped).
func (b *Broker) Subscribe(tenantID string) *Subscriber {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.nextID++
	id := time.Now().UTC().Format("20060102T150405.000000000") + "-" + itoa(b.nextID)
	sub := &Subscriber{
		id:   id,
		ch:   make(chan Event, bufferSize),
		done: make(chan struct{}),
	}
	bucket, ok := b.subscribers[tenantID]
	if !ok {
		bucket = make(map[*Subscriber]struct{})
		b.subscribers[tenantID] = bucket
	}
	bucket[sub] = struct{}{}
	return sub
}

// Publish sends ev to every subscriber on tenantID. Slow subscribers
// (full buffer) are dropped from the broker and their Done channel
// closed so the handler can react.
//
// Returns the number of subscribers the event was delivered to.
func (b *Broker) Publish(tenantID string, ev Event) int {
	b.mu.RLock()
	bucket, ok := b.subscribers[tenantID]
	if !ok {
		b.mu.RUnlock()
		return 0
	}
	// Copy the slice under read lock so we don't hold it while sending.
	subs := make([]*Subscriber, 0, len(bucket))
	for s := range bucket {
		subs = append(subs, s)
	}
	b.mu.RUnlock()

	delivered := 0
	for _, s := range subs {
		select {
		case s.ch <- ev:
			delivered++
		default:
			// Slow consumer: prune.
			b.mu.Lock()
			if cur, ok := b.subscribers[tenantID]; ok {
				if _, still := cur[s]; still {
					delete(cur, s)
					if len(cur) == 0 {
						delete(b.subscribers, tenantID)
					}
				}
			}
			b.mu.Unlock()
			s.Close()
		}
	}
	return delivered
}

// Unsubscribe removes sub from tenantID and closes its Done channel.
// Idempotent.
func (b *Broker) Unsubscribe(tenantID string, sub *Subscriber) {
	b.mu.Lock()
	defer b.mu.Unlock()
	bucket, ok := b.subscribers[tenantID]
	if !ok {
		return
	}
	if _, ok := bucket[sub]; ok {
		delete(bucket, sub)
		sub.Close()
		if len(bucket) == 0 {
			delete(b.subscribers, tenantID)
		}
	}
}

// Count returns the number of subscribers on tenantID (for /health
// observability and tests).
func (b *Broker) Count(tenantID string) int {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return len(b.subscribers[tenantID])
}

// itoa is a tiny dependency-free int → string for IDs.
func itoa(n uint64) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}