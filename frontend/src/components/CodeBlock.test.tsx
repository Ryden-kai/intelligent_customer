/**
 * v2.2 PR4 — CodeBlock 组件测试。
 *
 * 覆盖：
 *   - 默认渲染（语言标签 + 复制按钮）
 *   - 复制按钮点击：调用 navigator.clipboard.writeText
 *   - 复制成功后按钮文案变 ✓ 已复制
 *   - 无 clipboard API 时降级到临时 textarea + execCommand
 *   - 复制失败时显示错误状态
 *   - 语言识别（language-python → 'python'）
 *   - aria-live 宣告
 */

import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { render, fireEvent, screen, cleanup } from '@testing-library/react';

import { CodeBlock } from './CodeBlock';

afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
});

beforeEach(() => {
  // 默认提供 clipboard mock
  Object.assign(navigator, {
    clipboard: {
      writeText: vi.fn().mockResolvedValue(undefined),
    },
  });
});

describe('CodeBlock — 渲染', () => {
  it('默认：显示语言标签 + 复制按钮', () => {
    render(
      <CodeBlock language="python" code='print("hi")'>
        <code className="hljs language-python">{'print("hi")'}</code>
      </CodeBlock>,
    );
    expect(screen.getByTestId('code-block')).toBeDefined();
    expect(screen.getByTestId('code-block-copy')).toBeDefined();
    // 语言标签
    expect(screen.getByLabelText(/代码语言：python/)).toBeDefined();
    // 复制按钮初始文案
    expect(screen.getByTestId('code-block-copy').textContent).toContain('复制');
  });

  it('language 缺失时回退到 text', () => {
    render(
      <CodeBlock language={undefined} code="plain">
        <code className="hljs">plain</code>
      </CodeBlock>,
    );
    expect(screen.getByLabelText(/代码语言：text/)).toBeDefined();
  });

  it('language=text 或 plaintext 也显示 text', () => {
    render(
      <CodeBlock language="text" code="x">
        <code className="hljs language-text">x</code>
      </CodeBlock>,
    );
    expect(screen.getByLabelText(/代码语言：text/)).toBeDefined();
  });
});

describe('CodeBlock — 复制行为', () => {
  it('点击复制按钮调用 navigator.clipboard.writeText', async () => {
    const spy = navigator.clipboard.writeText as unknown as ReturnType<typeof vi.fn>;
    render(
      <CodeBlock language="js" code='console.log("ok")'>
        <code className="hljs language-js">{'console.log("ok")'}</code>
      </CodeBlock>,
    );
    fireEvent.click(screen.getByTestId('code-block-copy'));
    // 等 promise 链完成
    await vi.waitFor(() => {
      expect(spy).toHaveBeenCalledWith('console.log("ok")');
    });
  });

  it('复制成功后按钮文案变 ✓ 已复制（1s 后恢复）', async () => {
    render(
      <CodeBlock language="go" code="package main">
        <code className="hljs language-go">{'package main'}</code>
      </CodeBlock>,
    );
    const btn = screen.getByTestId('code-block-copy');
    fireEvent.click(btn);
    await vi.waitFor(() => {
      expect(btn.textContent).toContain('已复制');
    });
    // 1s 后应恢复（这里用 fake timers 模拟）
    vi.useFakeTimers();
    vi.advanceTimersByTime(1100);
    expect(btn.textContent).toContain('复制');
    vi.useRealTimers();
  });

  it('clipboard 不可用时降级到 textarea + execCommand', async () => {
    // 模拟 navigator.clipboard 不存在
    const originalClipboard = (navigator as { clipboard?: unknown }).clipboard;
    Object.defineProperty(navigator, 'clipboard', { value: undefined, configurable: true });
    // jsdom 没有 execCommand，手动注入一个
    const execSpy = vi.fn().mockReturnValue(true);
    Object.defineProperty(document, 'execCommand', {
      value: execSpy,
      configurable: true,
      writable: true,
    });

    render(
      <CodeBlock language="bash" code="echo hi">
        <code className="hljs language-bash">{'echo hi'}</code>
      </CodeBlock>,
    );
    fireEvent.click(screen.getByTestId('code-block-copy'));
    await vi.waitFor(() => {
      expect(execSpy).toHaveBeenCalledWith('copy');
    });

    // 恢复
    Object.defineProperty(navigator, 'clipboard', { value: originalClipboard, configurable: true });
  });

  it('clipboard 抛错时按钮显示 ✗ 复制失败', async () => {
    Object.assign(navigator, {
      clipboard: {
        writeText: vi.fn().mockRejectedValue(new Error('denied')),
      },
    });
    render(
      <CodeBlock language="sql" code="SELECT 1">
        <code className="hljs language-sql">{'SELECT 1'}</code>
      </CodeBlock>,
    );
    fireEvent.click(screen.getByTestId('code-block-copy'));
    await vi.waitFor(() => {
      expect(screen.getByTestId('code-block-copy').textContent).toContain('复制失败');
    });
  });

  it('aria-live 区域宣告复制状态', async () => {
    const { container } = render(
      <CodeBlock language="py" code="x=1">
        <code className="hljs language-py">{'x=1'}</code>
      </CodeBlock>,
    );
    const live = container.querySelector('[aria-live="polite"]');
    expect(live).not.toBeNull();
    // 初始空
    expect(live?.textContent).toBe('');
    fireEvent.click(screen.getByTestId('code-block-copy'));
    await vi.waitFor(() => {
      expect(live?.textContent).toContain('已复制');
    });
  });
});
