/**
 * v2.2 PR3 — ErrorBoundary 组件测试。
 *
 * v2.2 PR1 时用 `@ts-nocheck` 占位；PR3 装好 vitest 后移除并补全用例。
 */

import { describe, it, expect, vi } from 'vitest';
import { render, screen } from '@testing-library/react';
import { ErrorBoundary } from './ErrorBoundary';

// 故意抛错的子组件，用于触发 ErrorBoundary。
function Boom({ shouldThrow }: { shouldThrow: boolean }) {
  if (shouldThrow) {
    throw new Error('boom');
  }
  return <div data-testid="ok">正常子组件</div>;
}

describe('ErrorBoundary', () => {
  it('happy path: 子组件正常时透传渲染', () => {
    render(
      <ErrorBoundary>
        <Boom shouldThrow={false} />
      </ErrorBoundary>,
    );
    expect(screen.getByTestId('ok')).toBeInTheDocument();
    expect(screen.queryByText('页面出错了')).not.toBeInTheDocument();
  });

  it('error path: 子组件抛错时显示默认 fallback', () => {
    // 抑制 React 的错误日志（避免污染测试输出）。
    const consoleErrorSpy = vi.spyOn(console, 'error').mockImplementation(() => undefined);
    render(
      <ErrorBoundary scope="user">
        <Boom shouldThrow={true} />
      </ErrorBoundary>,
    );
    expect(screen.getByText('页面出错了')).toBeInTheDocument();
    expect(screen.getByText('刷新页面')).toBeInTheDocument();
    expect(screen.getByText(/错误编号：ERR-user-/)).toBeInTheDocument();
    consoleErrorSpy.mockRestore();
  });

  it('custom fallback: 调用方传入时优先使用', () => {
    const consoleErrorSpy = vi.spyOn(console, 'error').mockImplementation(() => undefined);
    render(
      <ErrorBoundary fallback={<div data-testid="custom">自定义降级</div>}>
        <Boom shouldThrow={true} />
      </ErrorBoundary>,
    );
    expect(screen.getByTestId('custom')).toBeInTheDocument();
    expect(screen.queryByText('页面出错了')).not.toBeInTheDocument();
    consoleErrorSpy.mockRestore();
  });

  it('scope: 不同 scope 生成不同的错误编号前缀', () => {
    const consoleErrorSpy = vi.spyOn(console, 'error').mockImplementation(() => undefined);
    const { unmount } = render(
      <ErrorBoundary scope="admin">
        <Boom shouldThrow={true} />
      </ErrorBoundary>,
    );
    expect(screen.getByText(/错误编号：ERR-admin-/)).toBeInTheDocument();
    unmount();
    consoleErrorSpy.mockRestore();
  });

  it('errorId 唯一：两次抛错应生成不同错误编号', () => {
    const consoleErrorSpy = vi.spyOn(console, 'error').mockImplementation(() => undefined);
    const { unmount } = render(
      <ErrorBoundary>
        <Boom shouldThrow={true} />
      </ErrorBoundary>,
    );
    const first = screen.getByText(/错误编号：ERR-/).textContent;
    unmount();
    render(
      <ErrorBoundary>
        <Boom shouldThrow={true} />
      </ErrorBoundary>,
    );
    const second = screen.getByText(/错误编号：ERR-/).textContent;
    expect(first).not.toBe(second);
    consoleErrorSpy.mockRestore();
  });
});
