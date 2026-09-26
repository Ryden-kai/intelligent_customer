// LiveBadge — visual indicator for the SSE realtime refresh status.
//
// Renders one of four states:
//   - connected: green pulsing dot + "实时"
//   - polling:   amber spinning dot + "轮询"
//   - offline:   grey dot + "离线"
//   - error:     red dot + error count

import React from 'react';

export interface LiveBadgeProps {
  /** True after the SSE connection (or polling fallback) is established. */
  connected: boolean;
  /** True when running on the polling fallback. */
  polling: boolean;
  /** Optional error message. */
  error?: string;
  /** ISO timestamp of the last successful update. */
  lastUpdate?: string;
  className?: string;
}

export function LiveBadge({
  connected,
  polling,
  error,
  lastUpdate,
  className,
}: LiveBadgeProps) {
  let bg = 'bg-slate-100 text-slate-600';
  let dot = 'bg-slate-400';
  let label = '离线';
  let animate = false;

  if (error && !connected) {
    bg = 'bg-danger-50 text-danger-700';
    dot = 'bg-danger-500';
    label = '连接异常';
  } else if (polling) {
    bg = 'bg-warning-50 text-warning-700';
    dot = 'bg-warning-500';
    label = '轮询';
    animate = true;
  } else if (connected) {
    bg = 'bg-success-50 text-success-700';
    dot = 'bg-success-500';
    label = '实时';
    animate = true;
  }

  const time = lastUpdate
    ? new Date(lastUpdate).toLocaleTimeString('zh-CN', { hour12: false })
    : null;

  return (
    <span
      className={`inline-flex items-center gap-2 px-2 py-1 rounded-full text-xs ${bg} ${className || ''}`}
      role="status"
      aria-live="polite"
      aria-label={error ? `实时刷新：${error}` : `实时刷新：${label}`}
    >
      <span
        className={`inline-block w-2 h-2 rounded-full ${dot} ${animate ? 'animate-pulse' : ''}`}
        aria-hidden="true"
      />
      <span>{label}</span>
      {time && <span className="text-app-text-muted">· {time}</span>}
      {error && <span className="text-danger-700 truncate max-w-[160px]">{error}</span>}
    </span>
  );
}

export default LiveBadge;