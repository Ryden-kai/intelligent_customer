/**
 * v2.2 PR3 — 设计令牌（design tokens）。
 *
 * 单一来源：所有颜色 / 间距 / 字体 / 圆角 / 阴影 / 字号 / 行高
 * 都从本文件导出，由 tailwind.config.ts 通过 theme.extend 消费。
 *
 * 设计原则：
 *   1. 颜色用 5 个语义族（brand / slate / semantic）；禁止散落 hex。
 *   2. 间距以 4px 为基准（PRD §4.1.1）。
 *   3. 圆角 / 阴影分层明确，避免每个组件单独写。
 *   4. 字体栈覆盖中文 + 英文 + 等宽，避免后加载。
 *   5. 全部 `as const`，键名稳定（供 types.ts 与组件 import）。
 *
 * 暗色策略：
 *   - 每个语义色同时给出 light/dark 双值，tailwind.config 转换时映射
 *     `bg-app / text-app / border-app` 三组 CSS 变量。
 *   - 在 index.css 的 `:root` / `.dark` 内覆写 CSS 变量。
 *   - 不使用 Tailwind darkMode:'media'，因为用户需要手动覆盖。
 */

// ---------------------------------------------------------------------------
// 颜色：brand / slate / semantic
// ---------------------------------------------------------------------------

export const colors = {
  brand: {
    50: '#eff6ff',
    100: '#dbeafe',
    200: '#bfdbfe',
    300: '#93c5fd',
    400: '#60a5fa',
    500: '#3b82f6',
    600: '#2563eb',
    700: '#1d4ed8',
    800: '#1e40af',
    900: '#1e3a8a',
  },
  slate: {
    50: '#f8fafc',
    100: '#f1f5f9',
    200: '#e2e8f0',
    300: '#cbd5e1',
    400: '#94a3b8',
    500: '#64748b',
    600: '#475569',
    700: '#334155',
    800: '#1e293b',
    900: '#0f172a',
    950: '#020617',
  },
  semantic: {
    success: { 50: '#ecfdf5', 500: '#10b981', 600: '#059669', 700: '#047857' },
    warning: { 50: '#fffbeb', 500: '#f59e0b', 600: '#d97706', 700: '#b45309' },
    danger: { 50: '#fef2f2', 500: '#ef4444', 600: '#dc2626', 700: '#b91c1c' },
    info: { 50: '#eff6ff', 500: '#3b82f6', 600: '#2563eb', 700: '#1d4ed8' },
  },
} as const;

// 暗色模式专用的"语义别名"。index.css 用这些 CSS 变量提供一站式 dark 覆写。
export const semanticAlias = {
  light: {
    bg: '#f8fafc', // slate-50 — 页面背景
    surface: '#ffffff', // 卡片/对话框背景
    surfaceMuted: '#f1f5f9', // 次级背景
    border: '#e2e8f0', // slate-200
    text: '#0f172a', // slate-900
    textMuted: '#64748b', // slate-500
    brand: '#3b82f6', // brand-500
  },
  dark: {
    bg: '#0f172a', // slate-900 — 页面背景
    surface: '#1e293b', // slate-800
    surfaceMuted: '#334155', // slate-700
    border: '#334155', // slate-700
    text: '#f1f5f9', // slate-100
    textMuted: '#94a3b8', // slate-400
    brand: '#60a5fa', // brand-400（暗色下亮度更高）
  },
} as const;

// ---------------------------------------------------------------------------
// 间距（4px 基准）
// ---------------------------------------------------------------------------

export const spacing = {
  0: '0',
  1: '0.25rem', // 4
  2: '0.5rem', // 8
  3: '0.75rem', // 12
  4: '1rem', // 16
  5: '1.25rem', // 20
  6: '1.5rem', // 24
  8: '2rem', // 32
  10: '2.5rem', // 40
  12: '3rem', // 48
  16: '4rem', // 64
  20: '5rem', // 80
  24: '6rem', // 96
} as const;

// ---------------------------------------------------------------------------
// 圆角（5 档）
// ---------------------------------------------------------------------------

export const radius = {
  none: '0',
  sm: '0.25rem', // 4 — 按钮 / 输入框
  md: '0.5rem', // 8 — 卡片
  lg: '0.75rem', // 12 — 弹窗
  xl: '1rem', // 16 — 大弹窗
  '2xl': '1.5rem', // 24 — 顶部气泡
  full: '9999px', // 头像 / 圆形按钮
} as const;

// ---------------------------------------------------------------------------
// 阴影（4 档 × 亮 / 暗 两组）
// ---------------------------------------------------------------------------

export const shadow = {
  sm: '0 1px 2px 0 rgba(0, 0, 0, 0.05)',
  md: '0 4px 6px -1px rgba(0, 0, 0, 0.07), 0 2px 4px -2px rgba(0, 0, 0, 0.04)',
  lg: '0 10px 15px -3px rgba(0, 0, 0, 0.10), 0 4px 6px -4px rgba(0, 0, 0, 0.05)',
  xl: '0 20px 25px -5px rgba(0, 0, 0, 0.12), 0 8px 10px -6px rgba(0, 0, 0, 0.04)',
  'dark-sm': '0 1px 2px 0 rgba(0, 0, 0, 0.3)',
  'dark-md': '0 4px 6px -1px rgba(0, 0, 0, 0.5), 0 2px 4px -2px rgba(0, 0, 0, 0.3)',
  'dark-lg': '0 10px 15px -3px rgba(0, 0, 0, 0.6), 0 4px 6px -4px rgba(0, 0, 0, 0.4)',
  'dark-xl': '0 20px 25px -5px rgba(0, 0, 0, 0.7), 0 8px 10px -6px rgba(0, 0, 0, 0.5)',
} as const;

// ---------------------------------------------------------------------------
// 字体 / 字号 / 行高 / 字重
// ---------------------------------------------------------------------------

export const typography = {
  fontFamily: {
    sans: [
      'Inter',
      '-apple-system',
      'BlinkMacSystemFont',
      '"PingFang SC"',
      '"Hiragino Sans GB"',
      '"Microsoft YaHei"',
      '"Helvetica Neue"',
      'Arial',
      'sans-serif',
    ],
    mono: [
      '"JetBrains Mono"',
      '"Fira Code"',
      'Menlo',
      'Monaco',
      'Consolas',
      '"Courier New"',
      'monospace',
    ],
  },
  fontSize: {
    xs: ['0.75rem', { lineHeight: '1rem' }], // 12 / 16
    sm: ['0.875rem', { lineHeight: '1.25rem' }], // 14 / 20
    base: ['1rem', { lineHeight: '1.5rem' }], // 16 / 24
    lg: ['1.125rem', { lineHeight: '1.75rem' }], // 18 / 28
    xl: ['1.25rem', { lineHeight: '1.75rem' }], // 20 / 28
    '2xl': ['1.5rem', { lineHeight: '2rem' }], // 24 / 32
    '3xl': ['1.875rem', { lineHeight: '2.25rem' }], // 30 / 36
    '4xl': ['2.25rem', { lineHeight: '2.5rem' }], // 36 / 40
  },
  fontWeight: {
    normal: '400',
    medium: '500',
    semibold: '600',
    bold: '700',
  },
  lineHeight: {
    tight: '1.25',
    normal: '1.5',
    relaxed: '1.75',
  },
} as const;

// ---------------------------------------------------------------------------
// 断点（与 PRD §2.1 / tailwind 默认对齐）
// ---------------------------------------------------------------------------

export const breakpoints = {
  mobile: '0', // < 640
  sm: '640px', // tablet 起点
  md: '768px',
  lg: '1024px', // desktop 起点
  xl: '1280px',
  '2xl': '1536px',
} as const;

// ---------------------------------------------------------------------------
// 聚合导出（便于 tree-shake）
// ---------------------------------------------------------------------------

export const tokens = {
  colors,
  semanticAlias,
  spacing,
  radius,
  shadow,
  typography,
  breakpoints,
} as const;

export type Tokens = typeof tokens;
