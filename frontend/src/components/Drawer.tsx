/**
 * v2.2 PR4 — 移动端抽屉组件。
 *
 * 用途：
 *   - 在 ≤ 640px 移动端展示侧栏（会话列表 / 菜单 / 帮助）。
 *   - 桌面端用普通侧栏（≥ 1024px），不再使用 Drawer。
 *
 * 行为：
 *   - 从屏幕左侧滑入（宽度 280px 或 80% viewport，取较小）。
 *   - 点击 backdrop（遮罩）关闭。
 *   - ESC 键关闭。
 *   - 打开时锁定 body 滚动。
 *   - 关闭 / 打开动画（CSS transition 200ms）。
 *
 * a11y：
 *   - role="dialog" + aria-modal="true"。
 *   - aria-labelledby 指向 title 元素。
 *   - 焦点移到抽屉内（默认首个 focusable）。
 */

import { useEffect, useRef } from 'react';

export interface DrawerProps {
  open: boolean;
  onClose: () => void;
  title?: string;
  titleId?: string;
  children: React.ReactNode;
}

export function Drawer({ open, onClose, title, titleId, children }: DrawerProps) {
  const panelRef = useRef<HTMLDivElement>(null);

  // ESC 键关闭
  useEffect(() => {
    if (!open) return;
    function handleKey(e: KeyboardEvent) {
      if (e.key === 'Escape') {
        e.stopPropagation();
        onClose();
      }
    }
    window.addEventListener('keydown', handleKey);
    return () => window.removeEventListener('keydown', handleKey);
  }, [open, onClose]);

  // body 滚动锁定
  useEffect(() => {
    if (!open) return;
    const prev = document.body.style.overflow;
    document.body.style.overflow = 'hidden';
    return () => {
      document.body.style.overflow = prev;
    };
  }, [open]);

  // 打开时聚焦首个 focusable
  useEffect(() => {
    if (!open || !panelRef.current) return;
    const focusable = panelRef.current.querySelector<HTMLElement>(
      'button, [href], input, select, textarea, [tabindex]:not([tabindex="-1"])',
    );
    focusable?.focus();
  }, [open]);

  if (!open) return null;

  return (
    <div
      role="dialog"
      aria-modal="true"
      aria-labelledby={titleId}
      className="fixed inset-0 z-40 lg:hidden"
      data-testid="drawer"
    >
      {/* 遮罩 */}
      <button
        type="button"
        aria-label="关闭抽屉"
        onClick={onClose}
        className="absolute inset-0 bg-black/50 transition-opacity"
        data-testid="drawer-backdrop"
      />
      {/* 抽屉面板 */}
      <div
        ref={panelRef}
        className="absolute left-0 top-0 h-full w-[280px] max-w-[80vw] bg-app-surface border-r border-app-border shadow-lg flex flex-col transition-transform"
        data-testid="drawer-panel"
      >
        {title && (
          <div className="px-4 py-3 border-b border-app-border flex items-center justify-between">
            <h2 id={titleId} className="text-sm font-semibold text-app-text">
              {title}
            </h2>
            <button
              type="button"
              onClick={onClose}
              aria-label="关闭"
              className="text-app-text-muted hover:text-app-text px-2 py-1 rounded focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-app-brand"
            >
              ✕
            </button>
          </div>
        )}
        <div className="flex-1 overflow-y-auto">{children}</div>
      </div>
    </div>
  );
}

export default Drawer;
