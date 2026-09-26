/**
 * v2.2 PR3 — ThemeToggle 组件。
 *
 * 三态切换按钮：system → light → dark → system。
 *   - 显示图标 + label（system 时显示当前 OS 偏好）
 *   - aria-label：缺省时屏幕阅读器友好
 *   - focus-visible：使用统一基线（focus ring）
 *
 * 用法：
 *   <ThemeToggle />                       — 默认按钮（适合 TopBar）
 *   <ThemeToggle className="ml-2" />      — 自定义样式
 */

import { useTheme, type ThemeMode } from '../hooks/useTheme';

interface Props {
  /** 注入额外 className */
  className?: string;
  /** 显示文字标签（默认 false，仅图标） */
  showLabel?: boolean;
}

const ICONS: Record<ThemeMode, string> = {
  system: '🖥', // 💻
  light: '☀',
  dark: '🌙',
};

const LABELS: Record<ThemeMode, string> = {
  system: '跟随系统',
  light: '浅色',
  dark: '深色',
};

const NEXT_LABEL: Record<ThemeMode, string> = {
  system: '切换到浅色',
  light: '切换到深色',
  dark: '切换到跟随系统',
};

export function ThemeToggle({ className = '', showLabel = false }: Props) {
  const { mode, cycleMode } = useTheme();

  return (
    <button
      type="button"
      onClick={cycleMode}
      aria-label={`主题：${LABELS[mode]}（${NEXT_LABEL[mode]}）`}
      title={`主题：${LABELS[mode]}（点击 ${NEXT_LABEL[mode]}）`}
      data-testid="theme-toggle"
      data-theme-mode={mode}
      className={
        'inline-flex items-center gap-1.5 px-2 py-1 text-sm rounded-md ' +
        'border border-app-border hover:bg-app-surface-muted ' +
        'focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-app-brand ' +
        'transition-colors ' +
        className
      }
    >
      <span aria-hidden="true" className="text-base leading-none">
        {ICONS[mode]}
      </span>
      {showLabel && (
        <span className="text-app-text-muted text-xs">{LABELS[mode]}</span>
      )}
    </button>
  );
}

export default ThemeToggle;
