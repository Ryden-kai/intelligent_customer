/**
 * v2.2 PR3 — RateLimitConfigPage 渲染测试。
 */

import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, waitFor } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';

vi.mock('../api', () => ({
  api: {
    ratelimit: {
      list: vi.fn().mockResolvedValue({
        items: [
          {
            id: 'rlc-login',
            tenant_id: 'tnt_default',
            endpoint: 'POST /api/auth/login',
            dimension: 'ip',
            per_minute: 10,
            per_hour: 100,
            burst: 5,
            enabled: true,
            description: '登录限流',
          },
        ],
        total: 1,
      }),
    },
  },
}));

import RateLimitConfigPage from './RateLimitConfigPage';

describe('RateLimitConfigPage', () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it('渲染标题与说明', () => {
    render(
      <MemoryRouter>
        <RateLimitConfigPage />
      </MemoryRouter>,
    );
    expect(screen.getByRole('heading', { name: /限流配置/ })).toBeInTheDocument();
    expect(screen.getByText(/token bucket/)).toBeInTheDocument();
  });

  it('加载完成后显示端点 + 编辑按钮', async () => {
    render(
      <MemoryRouter>
        <RateLimitConfigPage />
      </MemoryRouter>,
    );
    await waitFor(() => {
      expect(screen.getByText('POST /api/auth/login')).toBeInTheDocument();
    });
    expect(screen.getByRole('button', { name: /编辑 POST/ })).toBeInTheDocument();
  });
});
