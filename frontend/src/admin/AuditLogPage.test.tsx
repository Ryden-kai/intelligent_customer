/**
 * v2.2 PR3 — AuditLogPage 渲染测试。
 */

import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, waitFor } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';

vi.mock('../api', () => ({
  api: {
    audit: {
      list: vi.fn().mockResolvedValue({
        items: [
          {
            id: 'log-1',
            tenant_id: 'tnt_default',
            timestamp: new Date().toISOString(),
            actor_id: 'admin@demo',
            actor_email: 'admin@demo',
            action: 'auth.login',
            ip: '127.0.0.1',
          },
        ],
        total: 1,
        limit: 50,
        offset: 0,
      }),
    },
  },
}));

import AuditLogPage from './AuditLogPage';

describe('AuditLogPage', () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it('渲染标题 + 导出 CSV 按钮 + 5 个筛选字段', async () => {
    render(
      <MemoryRouter>
        <AuditLogPage />
      </MemoryRouter>,
    );
    expect(screen.getByRole('heading', { name: /审计日志/ })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: /导出 CSV/ })).toBeInTheDocument();
    // 5 个 label：起始时间 / 结束时间 / 操作者 ID / 动作 / 目标类型
    expect(screen.getByLabelText(/起始时间/)).toBeInTheDocument();
    expect(screen.getByLabelText(/结束时间/)).toBeInTheDocument();
    expect(screen.getByLabelText(/操作者 ID/)).toBeInTheDocument();
    expect(screen.getByLabelText(/动作/)).toBeInTheDocument();
    expect(screen.getByLabelText(/目标类型/)).toBeInTheDocument();
  });

  it('加载完成后显示日志行（auth.login）', async () => {
    render(
      <MemoryRouter>
        <AuditLogPage />
      </MemoryRouter>,
    );
    await waitFor(() => {
      expect(screen.getByText('auth.login')).toBeInTheDocument();
    });
  });
});
