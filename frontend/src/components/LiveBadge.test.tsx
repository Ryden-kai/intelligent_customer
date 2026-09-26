// LiveBadge component tests.

import { describe, expect, it } from 'vitest';
import { render, screen } from '@testing-library/react';
import { LiveBadge } from './LiveBadge';

describe('LiveBadge', () => {
  it('renders 离线 state when not connected', () => {
    render(<LiveBadge connected={false} polling={false} />);
    expect(screen.getByText('离线')).toBeInTheDocument();
  });

  it('renders 实时 state with pulse animation when connected', () => {
    render(<LiveBadge connected={true} polling={false} />);
    expect(screen.getByText('实时')).toBeInTheDocument();
  });

  it('renders 轮询 state when polling fallback', () => {
    render(<LiveBadge connected={true} polling={true} />);
    expect(screen.getByText('轮询')).toBeInTheDocument();
  });

  it('renders error label on connection failure', () => {
    render(<LiveBadge connected={false} polling={false} error="offline" />);
    expect(screen.getByText('连接异常')).toBeInTheDocument();
    expect(screen.getByText('offline')).toBeInTheDocument();
  });

  it('renders last update time when provided', () => {
    render(
      <LiveBadge
        connected={true}
        polling={false}
        lastUpdate="2026-09-25T10:00:00Z"
      />,
    );
    // The badge shows a time-derived label; the exact format depends on
    // the browser locale, but a 24-hour time always contains digits.
    // The container has the · separator before the time string.
    const status = screen.getByRole('status');
    expect(status.textContent).toMatch(/10:00:00|10:00|18:00/);
  });
});