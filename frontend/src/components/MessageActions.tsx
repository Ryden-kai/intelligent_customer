/**
 * v2.2 PR4 — 消息操作按钮组。
 *
 * 用途：在 assistant 消息右下角显示一组操作按钮（hover 才可见）。
 *
 * 3 类操作：
 *   1. 复制：把消息内容复制到剪贴板；成功后按钮变 ✓ 1s。
 *   2. 重新生成（regenerate）：重新发送最后一条 user 消息（由调用方实现）。
 *   3. 点赞 / 点踩（thumbsUp / thumbsDown）：调 api.feedback(rating)。
 *      - thumbUp → rating 5
 *      - thumbDown → rating 2
 *      - 已提交后按钮保持激活状态（再次点击可取消）。
 *
 * a11y：
 *   - 每个按钮有 aria-label；
 *   - 焦点环统一基线；
 *   - 反馈状态用 aria-pressed 表示。
 */

import { useCallback, useRef, useState } from 'react';

export type FeedbackState = 'none' | 'up' | 'down';

export interface MessageActionsProps {
  /** 消息全文（复制用） */
  content: string;
  /** 触发重新生成（重新发送最后一条 user 消息） */
  onRegenerate?: () => void;
  /** 触发点赞（thumbUp → rating=5） */
  onThumbUp?: () => void | Promise<void>;
  /** 触发点踩（thumbDown → rating=2） */
  onThumbDown?: () => void | Promise<void>;
  /** 禁用态（如 regenerate 期间） */
  disabled?: boolean;
  /** 当前反馈状态（受控） */
  feedback?: FeedbackState;
}

export function MessageActions({
  content,
  onRegenerate,
  onThumbUp,
  onThumbDown,
  disabled = false,
  feedback: feedbackProp,
}: MessageActionsProps) {
  const [copied, setCopied] = useState(false);
  const [copyError, setCopyError] = useState(false);
  const [internalFeedback, setInternalFeedback] = useState<FeedbackState>('none');
  const timerRef = useRef<number | null>(null);

  const feedback = feedbackProp ?? internalFeedback;
  const setFeedback = (next: FeedbackState) => {
    if (feedbackProp === undefined) {
      setInternalFeedback(next);
    }
  };

  const handleCopy = useCallback(async () => {
    try {
      if (typeof navigator !== 'undefined' && navigator.clipboard?.writeText) {
        await navigator.clipboard.writeText(content);
      } else if (typeof document !== 'undefined') {
        const ta = document.createElement('textarea');
        ta.value = content;
        ta.style.position = 'fixed';
        ta.style.opacity = '0';
        document.body.appendChild(ta);
        ta.select();
        document.execCommand('copy');
        document.body.removeChild(ta);
      } else {
        throw new Error('No clipboard API');
      }
      setCopied(true);
      setCopyError(false);
    } catch (e) {
      console.warn('MessageActions: copy failed', e);
      setCopyError(true);
    }
    if (timerRef.current) window.clearTimeout(timerRef.current);
    timerRef.current = window.setTimeout(() => {
      setCopied(false);
      setCopyError(false);
    }, 1000);
  }, [content]);

  const handleThumbUp = useCallback(async () => {
    if (disabled) return;
    const next: FeedbackState = feedback === 'up' ? 'none' : 'up';
    setFeedback(next);
    if (next === 'up') {
      await onThumbUp?.();
    }
  }, [disabled, feedback, onThumbUp]);

  const handleThumbDown = useCallback(async () => {
    if (disabled) return;
    const next: FeedbackState = feedback === 'down' ? 'none' : 'down';
    setFeedback(next);
    if (next === 'down') {
      await onThumbDown?.();
    }
  }, [disabled, feedback, onThumbDown]);

  return (
    <div
      role="toolbar"
      aria-label="消息操作"
      data-testid="message-actions"
      className="opacity-0 group-hover:opacity-100 focus-within:opacity-100 transition-opacity flex items-center gap-1 mt-1"
    >
      <button
        type="button"
        onClick={handleCopy}
        disabled={disabled}
        aria-label={copied ? '已复制' : copyError ? '复制失败' : '复制消息'}
        data-testid="msg-action-copy"
        title={copied ? '已复制' : '复制'}
        className="text-xs px-1.5 py-0.5 rounded text-app-text-muted hover:text-app-text hover:bg-app-surface-muted focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-app-brand disabled:opacity-40"
      >
        {copied ? '✓' : copyError ? '✗' : '📋'}
      </button>

      <button
        type="button"
        onClick={onRegenerate}
        disabled={disabled || !onRegenerate}
        aria-label="重新生成回复"
        data-testid="msg-action-regenerate"
        title="重新生成"
        className="text-xs px-1.5 py-0.5 rounded text-app-text-muted hover:text-app-text hover:bg-app-surface-muted focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-app-brand disabled:opacity-40"
      >
        🔄
      </button>

      <button
        type="button"
        onClick={handleThumbUp}
        disabled={disabled}
        aria-label="点赞"
        aria-pressed={feedback === 'up'}
        data-testid="msg-action-thumb-up"
        title="点赞"
        className={
          'text-xs px-1.5 py-0.5 rounded focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-app-brand disabled:opacity-40 ' +
          (feedback === 'up'
            ? 'text-success-600 bg-success-50 dark:bg-success-600/20'
            : 'text-app-text-muted hover:text-app-text hover:bg-app-surface-muted')
        }
      >
        👍
      </button>

      <button
        type="button"
        onClick={handleThumbDown}
        disabled={disabled}
        aria-label="点踩"
        aria-pressed={feedback === 'down'}
        data-testid="msg-action-thumb-down"
        title="点踩"
        className={
          'text-xs px-1.5 py-0.5 rounded focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-app-brand disabled:opacity-40 ' +
          (feedback === 'down'
            ? 'text-danger-600 bg-danger-50 dark:bg-danger-600/20'
            : 'text-app-text-muted hover:text-app-text hover:bg-app-surface-muted')
        }
      >
        👎
      </button>

      <span aria-live="polite" className="sr-only">
        {copied ? '消息已复制' : copyError ? '复制失败' : ''}
      </span>
    </div>
  );
}

export default MessageActions;
