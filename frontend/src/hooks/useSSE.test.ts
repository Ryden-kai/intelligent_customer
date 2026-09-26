// useSSE hook tests. We stub global EventSource + ResizeObserver in
// src/test/setup.ts so the hook can mount inside jsdom.

import { describe, expect, it, beforeEach, afterEach, vi } from 'vitest';
import { renderHook, act } from '@testing-library/react';
import { useSSE } from './useSSE';

type StubInstance = {
  url: string;
  onmessage: ((ev: MessageEvent) => void) | null;
  onerror: ((ev: Event) => void) | null;
  close: () => void;
  deliver: (data: string, type?: string) => void;
  fail: () => void;
};

let instances: StubInstance[] = [];

class StubEventSource {
  url: string;
  onmessage: ((ev: MessageEvent) => void) | null = null;
  onerror: ((ev: Event) => void) | null = null;
  constructor(url: string) {
    this.url = url;
    const self = this as unknown as StubInstance;
    self.deliver = (data: string, type: string = 'message') => {
      if (this.onmessage) {
        this.onmessage({ data, type } as unknown as MessageEvent);
      }
    };
    self.fail = () => {
      if (this.onerror) this.onerror(new Event('error'));
    };
    instances.push(self);
  }
  close() {
    /* no-op */
  }
}

beforeEach(() => {
  instances = [];
  (globalThis as any).EventSource = StubEventSource;
});
afterEach(() => {
  delete (globalThis as any).EventSource;
  vi.restoreAllMocks();
});

describe('useSSE', () => {
  it('connects and exposes events', () => {
    const { result } = renderHook(() => useSSE('/api/stream', {}));
    expect(instances).toHaveLength(1);
    act(() => {
      instances[0].deliver('hello');
    });
    expect(result.current.connected).toBe(true);
    expect(result.current.lastEvent?.payload).toBe('hello');
  });

  it('tracks retries when EventSource errors', () => {
    const { result } = renderHook(() =>
      useSSE('/api/stream', { retryMs: 10, maxRetries: 2 }),
    );
    act(() => instances[0].fail());
    expect(result.current.connected).toBe(false);
    expect(result.current.error).toContain('SSE retry');
  });

  it('falls back to polling when max retries exceeded', async () => {
    const fetchSpy = vi.fn().mockResolvedValue({
      ok: true,
      text: async () => '{"value":1}',
    });
    (globalThis as any).fetch = fetchSpy;

    const { result } = renderHook(() =>
      useSSE('/api/stream', {
        retryMs: 5,
        maxRetries: 1,
        pollingUrl: '/api/poll',
        pollingIntervalMs: 20,
      }),
    );
    // Trigger the first error → fail counter 1.
    act(() => instances[0].fail());
    // Trigger again → exceeds max → falls back to polling.
    act(() => instances[0].fail());
    expect(result.current.polling).toBe(true);

    await act(async () => {
      await new Promise((r) => setTimeout(r, 50));
    });
    expect(fetchSpy).toHaveBeenCalled();
  });

  it('reconnect() resets retry counter', () => {
    const { result } = renderHook(() =>
      useSSE('/api/stream', { retryMs: 5, maxRetries: 3 }),
    );
    act(() => instances[0].fail());
    expect(result.current.connected).toBe(false);
    act(() => result.current.reconnect());
    expect(result.current.error).toBeUndefined();
  });

  it('does not reconnect after unmount', () => {
    const { unmount } = renderHook(() => useSSE('/api/stream', {}));
    expect(instances).toHaveLength(1);
    unmount();
    // StubEventSource.close() called; no new instances.
    expect(instances).toHaveLength(1);
  });
});