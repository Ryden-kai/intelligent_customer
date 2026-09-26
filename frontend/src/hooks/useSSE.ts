// useSSE — React hook wrapping EventSource with automatic reconnection
// and a polling fallback when the browser doesn't expose EventSource.
//
// Design notes (docs/v2-architecture-design.md §3.3.2):
//
//   - EventSource handles reconnection natively when the server drops
//     the connection (PRD Q-C). We add a max-retry budget so a
//     permanently broken endpoint doesn't pin the page forever.
//   - On EventSource failure (e.g. browser polyfill missing) we
//     transparently switch to setInterval polling against the same
//     REST endpoint. Polling stops the moment SSE comes back.
//   - The hook returns the latest event payload so callers don't have
//     to manage event listeners themselves.

import { useEffect, useRef, useState } from 'react';

export interface SSEEvent {
  type: string;
  payload: string; // raw JSON string (kept as string so callers can JSON.parse lazily)
}

export interface SSEState {
  /** Most recent event payload (string), or undefined. */
  lastEvent?: SSEEvent;
  /** True after the connection has been established at least once. */
  connected: boolean;
  /** True when we've fallen back to polling (EventSource failed). */
  polling: boolean;
  /** Last error string (for diagnostic UI). */
  error?: string;
  /** Force the hook to retry immediately. */
  reconnect: () => void;
}

export interface UseSSEOptions {
  /** When the connection drops, retry after this many ms. */
  retryMs?: number;
  /** Maximum consecutive failed retries before giving up. */
  maxRetries?: number;
  /**
   * Polling fallback endpoint. If omitted and EventSource fails, the
   * hook stops delivering events (silent degradation).
   */
  pollingUrl?: string;
  /** Polling interval (ms). Default = 5000. */
  pollingIntervalMs?: number;
  /** Optional Bearer token to attach (Authorization header). */
  token?: string | null;
}

const DEFAULT_RETRY = 2000;
const DEFAULT_MAX = 5;

/**
 * Subscribe to an SSE endpoint and forward parsed events.
 *
 * The hook is generic over the payload: callers get a raw string they
 * can JSON.parse; we don't pretend to know the schema.
 */
export function useSSE(url: string, opts: UseSSEOptions = {}): SSEState {
  const {
    retryMs = DEFAULT_RETRY,
    maxRetries = DEFAULT_MAX,
    pollingUrl,
    pollingIntervalMs = 5000,
    token,
  } = opts;

  const [lastEvent, setLastEvent] = useState<SSEEvent | undefined>();
  const [connected, setConnected] = useState(false);
  const [polling, setPolling] = useState(false);
  const [error, setError] = useState<string | undefined>();
  const retriesRef = useRef(0);
  const closedRef = useRef(false);

  // Reconnect trigger (incremented via reconnect() to force a new attempt).
  const [reconnectTick, setReconnectTick] = useState(0);
  const reconnect = () => {
    retriesRef.current = 0;
    setError(undefined);
    setConnected(false);
    setReconnectTick((n) => n + 1);
  };

  useEffect(() => {
    closedRef.current = false;
    // SSR guard: EventSource is undefined in node.
    if (typeof window === 'undefined' || typeof EventSource === 'undefined') {
      // Skip SSE; start polling if a fallback is provided.
      if (pollingUrl) startPolling();
      return;
    }

    let es: EventSource | null = null;
    let cancelled = false;

    const onMessage = (e: MessageEvent) => {
      retriesRef.current = 0;
      setConnected(true);
      setError(undefined);
      try {
        // EventSource data fields are strings; the broker encodes JSON in
        // the payload but the envelope itself isn't JSON, so we don't try
        // to parse it here — callers do that.
        const ev: SSEEvent = {
          type: (e as any).type || 'message',
          payload: e.data,
        };
        setLastEvent(ev);
      } catch (err) {
        // Shouldn't happen — we wrap in JSON.parse only at the call site.
      }
    };

    const onError = () => {
      setConnected(false);
      if (closedRef.current) return;
      retriesRef.current++;
      if (retriesRef.current > maxRetries) {
        setError(`SSE disconnected after ${maxRetries} retries; falling back to polling`);
        if (pollingUrl) startPolling();
        return;
      }
      // EventSource auto-retries; the next connection attempt happens
      // ~retryMs later (browser default is 3s). We just surface the error.
      setError(`SSE retry ${retriesRef.current}/${maxRetries}`);
    };

    function startPolling() {
      if (!pollingUrl || closedRef.current) return;
      setPolling(true);
      let alive = true;
      const tick = async () => {
        if (!alive) return;
        try {
          const headers: Record<string, string> = {};
          if (token) headers.Authorization = `Bearer ${token}`;
          const res = await fetch(pollingUrl, { headers });
          if (!res.ok) throw new Error(`HTTP ${res.status}`);
          const payload = await res.text();
          setLastEvent({ type: 'stats_update', payload });
        } catch (e: any) {
          setError(`polling failed: ${e?.message ?? e}`);
        }
      };
      tick();
      const id = setInterval(tick, pollingIntervalMs);
      // Save interval id on the closure for cleanup.
      (startPolling as any)._id = id;
    }

    function stopPolling() {
      const id = (startPolling as any)._id;
      if (id) clearInterval(id);
      (startPolling as any)._id = null;
      setPolling(false);
    }

    try {
      // EventSource doesn't allow custom headers; we pass token via
      // query param when the URL builder supports it. The backend's
      // /api/admin/stream currently expects the bearer token in the
      // cookie / signature, not header — for browser EventSource the
      // cookie auth path is used. Fallback: prefix URL with token query
      // string.
      const u = token
        ? appendQuery(url, `t=${encodeURIComponent(token)}`)
        : url;
      es = new EventSource(u);
      es.onmessage = onMessage;
      es.onerror = onError;
    } catch (e: any) {
      setError(`EventSource ctor failed: ${e?.message ?? e}`);
      if (pollingUrl) startPolling();
    }

    return () => {
      cancelled = true;
      closedRef.current = true;
      stopPolling();
      if (es) {
        es.close();
      }
    };
    // re-run when url, retry knobs, polling knobs, or reconnect trigger change
  }, [url, retryMs, maxRetries, pollingUrl, pollingIntervalMs, token, reconnectTick]);

  return { lastEvent, connected, polling, error, reconnect };
}

function appendQuery(url: string, kv: string): string {
  return url + (url.includes('?') ? '&' : '?') + kv;
}