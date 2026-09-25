/**
 * v2.2 PR4 — 代码块组件。
 *
 * 职责：
 *   1. 包裹 rehype-highlight 输出的 `<pre><code class="hljs language-xxx">`。
 *   2. 显示语言标签（右上角）。
 *   3. 提供 📋 复制按钮（navigator.clipboard.writeText）。
 *   4. 复制成功后按钮文案变 ✓ 1s，然后恢复。
 *
 * 安全：
 *   - 复制按钮接收的 code 已经是 rehype-sanitize 清洗过的安全 HTML 文本
 *     （来自 DOM 的 textContent，不含原始 <script> 等）。
 *
 * a11y：
 *   - 按钮含 aria-label；复制成功通过 aria-live="polite" 区域宣告。
 *   - 焦点环走全局 :focus-visible 基线。
 */

import { useCallback, useRef, useState } from 'react';

interface Props {
  /** 语言标识（由 rehype-highlight 从 className 抽取，可空） */
  language: string | undefined;
  /** 原始代码文本（来自 ReactMarkdown children，纯字符串） */
  code: string;
  /** 内部 <code> 元素的 className（含 hljs + language-xxx 等） */
  codeClassName?: string;
  /** 渲染 children（典型为 <code className={...}>{code}</code>） */
  children: React.ReactNode;
}

export function CodeBlock({ language, code, codeClassName, children }: Props) {
  const [copied, setCopied] = useState(false);
  const [copyError, setCopyError] = useState(false);
  const timerRef = useRef<number | null>(null);

  const handleCopy = useCallback(async () => {
    try {
      // 优先用现代 Clipboard API；不支持时降级到临时 textarea。
      if (typeof navigator !== 'undefined' && navigator.clipboard?.writeText) {
        await navigator.clipboard.writeText(code);
      } else if (typeof document !== 'undefined') {
        const ta = document.createElement('textarea');
        ta.value = code;
        ta.style.position = 'fixed';
        ta.style.opacity = '0';
        document.body.appendChild(ta);
        ta.select();
        document.execCommand('copy');
        document.body.removeChild(ta);
      } else {
        throw new Error('No clipboard API available');
      }
      setCopied(true);
      setCopyError(false);
    } catch (e) {
      console.warn('CodeBlock: copy failed', e);
      setCopyError(true);
    }
    if (timerRef.current) window.clearTimeout(timerRef.current);
    timerRef.current = window.setTimeout(() => {
      setCopied(false);
      setCopyError(false);
    }, 1000);
  }, [code]);

  const langLabel = language && language !== 'text' && language !== 'plaintext'
    ? language
    : 'text';

  return (
    <div className="ic-code-block" data-testid="code-block">
      <div className="ic-code-block-header">
        <span aria-label={`代码语言：${langLabel}`}>{langLabel}</span>
        <button
          type="button"
          onClick={handleCopy}
          aria-label={copied ? '已复制' : '复制代码'}
          title={copied ? '已复制' : '复制代码'}
          data-testid="code-block-copy"
          className="ic-code-block-copy"
        >
          {copied ? '✓ 已复制' : copyError ? '✗ 复制失败' : '📋 复制'}
        </button>
      </div>
      <pre>
        <code className={codeClassName ?? 'hljs'}>{children}</code>
      </pre>
      <span aria-live="polite" className="sr-only">
        {copied ? '代码已复制到剪贴板' : copyError ? '复制失败' : ''}
      </span>
    </div>
  );
}

export default CodeBlock;
