/**
 * v2.2 PR3 — Tailwind 配置（消费 design tokens）。
 *
 * 关键变化（相对 v2.1.1）：
 *   - darkMode: 'class'（由 useTheme 切 .dark class）
 *   - theme.extend 引用 tokens.ts
 *   - 增加 a11y focus ring plugin：focus-visible:ring-2 / focus-visible:ring-brand-500
 *
 * 注意：
 *   - JS 直接 import tokens.ts 会让 tailwind.config 同时被 Vite / Webpack
 *     加载；保持 .js 格式并通过静态路径 import，避免与 PostCSS 编译期冲突。
 *   - 暗色背景 / 文字仍走语义 utility：`bg-app-surface` /
 *     `text-app` / `border-app`，对应 :root / .dark 里的 CSS 变量。
 */

import {
  colors,
  semanticAlias,
  spacing,
  radius,
  shadow,
  typography,
  breakpoints,
} from './src/design/tokens';

/** @type {import('tailwindcss').Config} */
export default {
  content: ['./index.html', './src/**/*.{ts,tsx}'],
  darkMode: 'class',
  theme: {
    // 完全替换 screens：与 tokens.breakpoints 对齐（mobile/tablet/desktop 三档）。
    screens: {
      mobile: breakpoints.mobile,
      sm: breakpoints.sm,
      md: breakpoints.md,
      lg: breakpoints.lg,
      xl: breakpoints.xl,
      '2xl': breakpoints['2xl'],
    },
    extend: {
      colors: {
        brand: colors.brand,
        slate: colors.slate,
        success: colors.semantic.success,
        warning: colors.semantic.warning,
        danger: colors.semantic.danger,
        info: colors.semantic.info,
        // 应用级语义色：light/dark 由 index.css CSS 变量驱动。
        app: {
          bg: 'var(--app-bg)',
          surface: 'var(--app-surface)',
          'surface-muted': 'var(--app-surface-muted)',
          border: 'var(--app-border)',
          text: 'var(--app-text)',
          'text-muted': 'var(--app-text-muted)',
          brand: 'var(--app-brand)',
        },
      },
      borderRadius: {
        none: radius.none,
        sm: radius.sm,
        md: radius.md,
        lg: radius.lg,
        xl: radius.xl,
        '2xl': radius['2xl'],
        full: radius.full,
      },
      boxShadow: {
        sm: shadow.sm,
        md: shadow.md,
        lg: shadow.lg,
        xl: shadow.xl,
        'dark-sm': shadow['dark-sm'],
        'dark-md': shadow['dark-md'],
        'dark-lg': shadow['dark-lg'],
        'dark-xl': shadow['dark-xl'],
      },
      spacing: {
        0: spacing[0],
        1: spacing[1],
        2: spacing[2],
        3: spacing[3],
        4: spacing[4],
        5: spacing[5],
        6: spacing[6],
        8: spacing[8],
        10: spacing[10],
        12: spacing[12],
        16: spacing[16],
        20: spacing[20],
        24: spacing[24],
      },
      fontFamily: {
        sans: typography.fontFamily.sans,
        mono: typography.fontFamily.mono,
      },
      fontSize: typography.fontSize,
      fontWeight: typography.fontWeight,
      lineHeight: typography.lineHeight,
      // a11y focus ring（统一基线）
      ringColor: {
        DEFAULT: 'var(--app-brand)',
      },
      ringOffsetColor: {
        DEFAULT: 'var(--app-bg)',
      },
      // 引用但导出，仅给 JS 调用方使用。
      transitionDuration: {
        DEFAULT: '150ms',
      },
    },
  },
  plugins: [],
};

// 防止 tree-shake 报警（semanticAlias 通过 :root CSS 变量间接消费）
void semanticAlias;
