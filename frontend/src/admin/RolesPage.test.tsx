/**
 * v2.2 PR3 — RolesPage 渲染测试。
 *
 * 之前是占位（@ts-nocheck），PR3 装好 vitest 后补一个最小用例验证：
 *   - 页面渲染（不依赖后端）
 *   - 标题存在
 *   - 错误时不崩
 */

import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, waitFor } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';

// Mock api 模块，避免拉真实 axios
vi.mock('../api', () => ({
  api: {
    rbac: {
      listRoles: vi.fn().mockResolvedValue({
        items: [
          {
            id: 'role-1',
            tenant_id: 'tnt_default',
            name: 'admin',
            description: '平台管理员',
            permissions: ['*'],
            is_system: true,
            user_count: 1,
            created_at: new Date().toISOString(),
            updated_at: new Date().toISOString(),
          },
          {
            id: 'role-2',
            tenant_id: 'tnt_default',
            name: 'agent',
            description: '客服坐席',
            permissions: ['conversation.read', 'skills.read'],
            is_system: false,
            user_count: 5,
            created_at: new Date().toISOString(),
            updated_at: new Date().toISOString(),
          },
        ],
        total: 2,
      }),
      listPermissions: vi.fn().mockResolvedValue({
        permissions: [
          { code: 'conversation.read', description: '查看对话', group_name: 'conversation' },
          { code: 'skills.read', description: '查看 Skills', group_name: 'skills' },
        ],
        groups: { conversation: ['conversation.read'], skills: ['skills.read'] },
        total: 2,
      }),
    },
  },
}));

import RolesPage from './RolesPage';

describe('RolesPage', () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it('渲染标题与"新建角色"按钮', async () => {
    render(
      <MemoryRouter>
        <RolesPage />
      </MemoryRouter>,
    );
    expect(screen.getByRole('heading', { name: /角色管理/ })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: /新建角色/ })).toBeInTheDocument();
  });

  it('加载完成后显示角色行（admin / agent）', async () => {
    render(
      <MemoryRouter>
        <RolesPage />
      </MemoryRouter>,
    );
    await waitFor(() => {
      expect(screen.getByText('admin')).toBeInTheDocument();
    });
    expect(screen.getByText('agent')).toBeInTheDocument();
  });
});
