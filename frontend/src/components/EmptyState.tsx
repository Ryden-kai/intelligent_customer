/**
 * v2.2 PR3 — EmptyState 组件。
 *
 * 三态（PRD §5.7）：
 *   - noData      "暂无数据"   + 📭
 *   - noResult    "无匹配结果" + 🔍
 *   - unauthorized "需要登录" + 🔒
 *
 * 用法：
 *   <EmptyState variant="noData" title="暂无数据" />
 *   <EmptyState variant="noResult" title="无匹配结果" action={{ label: "清空筛选", onClick: clear }} />
 *   <EmptyState variant="unauthorized" title="请先登录" action={{ label: "登录", onClick: login }} />
 */

import type { ReactNode } from 'react';

export type EmptyStateVariant = 'noData' | 'noResult' | 'unauthorized';

export interface EmptyStateAction {
  /** 按钮文本 */
  label: string;
  /** 点击回调 */
  onClick: () => void;
  /** variant：primary / secondary（默认 primary） */
  variant?: 'primary' | 'secondary';
}

export interface EmptyStateProps {
  variant?: EmptyStateVariant;
  /** 主标题（默认按 variant 给文案） */
  title?: string;
  /** 副标题 / 描述 */
  description?: string;
  /** 自定义 emoji / icon（缺省走 variant 默认） */
  icon?: string;
  /** CTA 操作 */
  action?: EmptyStateAction;
  /** 自定义 className */
  className?: string;
  /** children 自定义内容（追加在 action 之前） */
  children?: ReactNode;
}

const DEFAULT_ICON: Record<EmptyStateVariant, string> = {
  noData: '📭',
  noResult: '🔍',
  unauthorized: '🔒',
};

const DEFAULT_TITLE: Record<EmptyStateVariant, string> = {
  noData: '暂无数据',
  noResult: '没有找到匹配结果',
  unauthorized: '请先登录',
};

const DEFAULT_DESC: Record<EmptyStateVariant, string> = {
  noData: '这里是空的。开始你的第一次操作吧。',
  noResult: '试试调整筛选条件，或者清空筛选。',
  unauthorized: '你需要登录才能访问此页面。',
};

const DEFAULT_ACTION: Record<EmptyStateVariant, EmptyStateAction | undefined> = {
  noData: undefined,
  noResult: { label: '清空筛选', onClick: () => undefined, variant: 'secondary' },
  unauthorized: { label: '登录', onClick: () => undefined, variant: 'primary' },
};

export function EmptyState({
  variant = 'noData',
  title,
  description,
  icon,
  action,
  className = '',
  children,
}: EmptyStateProps) {
  const finalIcon = icon ?? DEFAULT_ICON[variant];
  const finalTitle = title ?? DEFAULT_TITLE[variant];
  const finalDesc = description ?? DEFAULT_DESC[variant];
  const finalAction = action ?? DEFAULT_ACTION[variant];

  return (
    <div
      role="status"
      aria-live="polite"
      data-testid="empty-state"
      data-variant={variant}
      className={
        'flex flex-col items-center justify-center text-center py-10 px-6 ' +
        'border border-dashed border-app-border rounded-xl bg-app-surface ' +
        'text-app-text ' +
        className
      }
    >
      <div className="text-5xl mb-3 select-none" aria-hidden="true">
        {finalIcon}
      </div>
      <h3 className="text-base font-semibold text-app-text mb-1">{finalTitle}</h3>
      <p className="text-sm text-app-text-muted mb-4 max-w-md">{finalDesc}</p>
      {children}
      {finalAction && (
        <button
          type="button"
          onClick={finalAction.onClick}
          aria-label={finalAction.label}
          className={
            finalAction.variant === 'secondary'
              ? 'px-4 py-2 text-sm rounded-md border border-app-border text-app-text hover:bg-app-surface-muted focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-app-brand'
              : 'px-4 py-2 text-sm rounded-md bg-brand-500 hover:bg-brand-600 text-white focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-brand-500 focus-visible:ring-offset-2 focus-visible:ring-offset-app-bg'
          }
        >
          {finalAction.label}
        </button>
      )}
    </div>
  );
}

export default EmptyState;
