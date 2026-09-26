/**
 * v2.2 PR3 — Skeleton 单元测试。
 */

import { describe, it, expect } from 'vitest';
import { render, screen } from '@testing-library/react';
import { Skeleton } from './Skeleton';

describe('Skeleton', () => {
  it('默认 variant=text + width 100% + height 0.875rem', () => {
    render(<Skeleton />);
    const el = screen.getByTestId('skeleton-item');
    expect(el).toBeInTheDocument();
    expect(el.style.width).toBe('100%');
    // 默认 height = '0.875rem'（设计令牌 spacing/text-xs 等价 14px）
    expect(el.style.height).toBe('0.875rem');
  });

  it('variant=circle 渲染 rounded-full', () => {
    render(<Skeleton variant="circle" width={48} height={48} />);
    const el = screen.getByTestId('skeleton-item');
    expect(el.className).toMatch(/rounded-full/);
    expect(el.style.width).toBe('48px');
    expect(el.style.height).toBe('48px');
  });

  it('variant=rect 接受 number / string 高度', () => {
    const { rerender } = render(<Skeleton variant="rect" height={120} />);
    let el = screen.getByTestId('skeleton-item');
    expect(el.style.height).toBe('120px');
    rerender(<Skeleton variant="rect" height="8rem" />);
    el = screen.getByTestId('skeleton-item');
    expect(el.style.height).toBe('8rem');
  });

  it('animate=true（默认）包含 shimmer class', () => {
    render(<Skeleton />);
    const el = screen.getByTestId('skeleton-item');
    expect(el.className).toMatch(/ic-skeleton-shimmer/);
  });

  it('animate=false 不含 shimmer class', () => {
    render(<Skeleton animate={false} />);
    const el = screen.getByTestId('skeleton-item');
    expect(el.className).not.toMatch(/ic-skeleton-shimmer/);
  });

  it('count > 1 渲染多行（仅 text 形态生效）', () => {
    render(<Skeleton variant="text" count={3} />);
    const container = screen.getByTestId('skeleton');
    expect(container.children).toHaveLength(3);
  });

  it('count > 1 但 variant=rect 不展开', () => {
    render(<Skeleton variant="rect" count={3} />);
    const items = screen.getAllByTestId('skeleton-item');
    expect(items).toHaveLength(1);
  });

  it('aria-label 默认"加载中" + aria-busy=true + role=status', () => {
    render(<Skeleton />);
    const el = screen.getByTestId('skeleton-item');
    expect(el.getAttribute('aria-label')).toBe('加载中');
    expect(el.getAttribute('aria-busy')).toBe('true');
    expect(el.getAttribute('role')).toBe('status');
  });

  it('aria-label 可自定义', () => {
    render(<Skeleton ariaLabel="正在加载会话" />);
    expect(screen.getByLabelText('正在加载会话')).toBeInTheDocument();
  });

  it('className 注入有效', () => {
    render(<Skeleton className="my-skel" />);
    const el = screen.getByTestId('skeleton-item');
    expect(el.className).toMatch(/my-skel/);
  });
});
