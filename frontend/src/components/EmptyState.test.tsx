/**
 * v2.2 PR3 — EmptyState 单元测试。
 */

import { describe, it, expect, vi } from 'vitest';
import { render, screen, fireEvent } from '@testing-library/react';
import { EmptyState } from './EmptyState';

describe('EmptyState', () => {
  it('默认 variant=noData：📭 + 暂无数据', () => {
    render(<EmptyState />);
    const el = screen.getByTestId('empty-state');
    expect(el.getAttribute('data-variant')).toBe('noData');
    expect(screen.getByText('暂无数据')).toBeInTheDocument();
  });

  it('variant=noResult：🔍 + 没有找到匹配结果 + 默认"清空筛选"按钮', () => {
    render(<EmptyState variant="noResult" />);
    expect(screen.getByTestId('empty-state').getAttribute('data-variant')).toBe('noResult');
    expect(screen.getByText('没有找到匹配结果')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: '清空筛选' })).toBeInTheDocument();
  });

  it('variant=unauthorized：🔒 + 请先登录 + 默认"登录"按钮', () => {
    render(<EmptyState variant="unauthorized" />);
    expect(screen.getByTestId('empty-state').getAttribute('data-variant')).toBe('unauthorized');
    expect(screen.getByText('请先登录')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: '登录' })).toBeInTheDocument();
  });

  it('自定义 title / description / icon 覆盖默认', () => {
    render(
      <EmptyState
        title="自定义标题"
        description="自定义描述"
        icon="🎯"
      />,
    );
    expect(screen.getByText('自定义标题')).toBeInTheDocument();
    expect(screen.getByText('自定义描述')).toBeInTheDocument();
    expect(screen.getByText('🎯')).toBeInTheDocument();
  });

  it('action.onClick 被点击时触发', () => {
    const onClick = vi.fn();
    render(
      <EmptyState
        variant="noData"
        title="空"
        action={{ label: '新建', onClick }}
      />,
    );
    fireEvent.click(screen.getByRole('button', { name: '新建' }));
    expect(onClick).toHaveBeenCalledTimes(1);
  });

  it('action.variant=secondary 走 border 样式', () => {
    render(
      <EmptyState
        variant="noData"
        title="空"
        action={{ label: '次按钮', onClick: () => undefined, variant: 'secondary' }}
      />,
    );
    const btn = screen.getByRole('button', { name: '次按钮' });
    expect(btn.className).toMatch(/border/);
  });

  it('role=status + aria-live=polite', () => {
    render(<EmptyState />);
    const el = screen.getByTestId('empty-state');
    expect(el.getAttribute('role')).toBe('status');
    expect(el.getAttribute('aria-live')).toBe('polite');
  });

  it('children 自定义内容渲染', () => {
    render(
      <EmptyState variant="noData">
        <div data-testid="custom-child">说明文字</div>
      </EmptyState>,
    );
    expect(screen.getByTestId('custom-child')).toBeInTheDocument();
  });

  it('className 注入有效', () => {
    render(<EmptyState className="my-empty" />);
    expect(screen.getByTestId('empty-state').className).toMatch(/my-empty/);
  });

  it('action 不传时不渲染按钮', () => {
    render(<EmptyState variant="noData" title="空" />);
    expect(screen.queryByRole('button')).not.toBeInTheDocument();
  });
});
