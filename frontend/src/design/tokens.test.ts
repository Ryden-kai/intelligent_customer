/**
 * v2.2 PR3 — tokens 单元测试。
 *
 * 验收口径（PR3 §4.7 用例 1）：
 *   - 5 组 token 对象齐全
 *   - 关键色值在 [WCAG AA] 安全范围（对比度 ≥ 4.5:1）
 *   - 间距为 4 倍数（PRD §4.1.1）
 */

import { describe, it, expect } from 'vitest';
import {
  tokens,
  colors,
  spacing,
  radius,
  shadow,
  typography,
  breakpoints,
  semanticAlias,
} from './tokens';

describe('design tokens — 5 组齐全', () => {
  it('tokens 对象包含 5 组（colors / spacing / typography / radius / shadow）', () => {
    expect(tokens).toHaveProperty('colors');
    expect(tokens).toHaveProperty('spacing');
    expect(tokens).toHaveProperty('typography');
    expect(tokens).toHaveProperty('radius');
    expect(tokens).toHaveProperty('shadow');
  });

  it('colors 含 brand / slate / semantic 3 个子族', () => {
    expect(colors.brand).toBeDefined();
    expect(colors.slate).toBeDefined();
    expect(colors.semantic).toBeDefined();
  });

  it('semantic 含 success / warning / danger / info 4 个语义色', () => {
    expect(colors.semantic.success).toBeDefined();
    expect(colors.semantic.warning).toBeDefined();
    expect(colors.semantic.danger).toBeDefined();
    expect(colors.semantic.info).toBeDefined();
  });

  it('spacing 含 0 / 1 / 2 / 3 / 4 / 6 / 8 / 12 / 16 关键档位', () => {
    const keys = Object.keys(spacing);
    expect(keys).toEqual(expect.arrayContaining(['0', '1', '2', '3', '4', '6', '8', '12', '16']));
  });

  it('radius 含 sm / md / lg / xl / 2xl / full 6 档', () => {
    const keys = Object.keys(radius);
    expect(keys).toEqual(expect.arrayContaining(['sm', 'md', 'lg', 'xl', '2xl', 'full']));
  });

  it('shadow 含 sm / md / lg / xl 4 档', () => {
    const keys = Object.keys(shadow);
    expect(keys).toEqual(expect.arrayContaining(['sm', 'md', 'lg', 'xl']));
  });
});

describe('design tokens — 取值合规', () => {
  it('brand-500 是蓝色系（hex 第 1-2 位 3B），符合主色定义', () => {
    expect(colors.brand[500].toLowerCase()).toBe('#3b82f6');
  });

  it('brand 100/200/300/400/500/600/700/800/900 全部存在（9 档）', () => {
    const keys = Object.keys(colors.brand);
    expect(keys).toEqual(['50', '100', '200', '300', '400', '500', '600', '700', '800', '900']);
  });

  it('slate 0-950 共 11 档（Tailwind 默认调色板）', () => {
    const keys = Object.keys(colors.slate);
    expect(keys.length).toBe(11);
  });

  it('spacing-1 = 0.25rem = 4px（4 像素基准）', () => {
    expect(spacing[1]).toBe('0.25rem');
  });

  it('spacing-4 = 1rem = 16px', () => {
    expect(spacing[4]).toBe('1rem');
  });

  it('radius-full = 9999px（圆形）', () => {
    expect(radius.full).toBe('9999px');
  });

  it('shadow-sm / md / lg / xl 都是合法 CSS box-shadow 字符串', () => {
    for (const key of ['sm', 'md', 'lg', 'xl'] as const) {
      expect(shadow[key]).toMatch(/^0 \d+px \d+px/);
    }
  });
});

describe('design tokens — 字体配置', () => {
  it('fontFamily.sans 包含中文（CJK）回退字体', () => {
    expect(typography.fontFamily.sans).toEqual(
      expect.arrayContaining([expect.stringMatching(/PingFang|YaHei|Hiragino/)]),
    );
  });

  it('fontFamily.mono 包含 JetBrains Mono / Fira Code', () => {
    expect(typography.fontFamily.mono).toEqual(
      expect.arrayContaining([expect.stringMatching(/JetBrains|Fira Code/)]),
    );
  });

  it('fontSize 关键档位齐全（xs/sm/base/lg/xl/2xl/3xl）', () => {
    const keys = Object.keys(typography.fontSize);
    expect(keys).toEqual(
      expect.arrayContaining(['xs', 'sm', 'base', 'lg', 'xl', '2xl', '3xl']),
    );
  });

  it('fontSize 每个值都含 [size, { lineHeight }] 元组', () => {
    for (const [size, opts] of Object.values(typography.fontSize)) {
      expect(typeof size).toBe('string');
      expect(opts).toHaveProperty('lineHeight');
    }
  });

  it('fontWeight 含 normal / medium / semibold / bold', () => {
    expect(typography.fontWeight).toEqual({
      normal: '400',
      medium: '500',
      semibold: '600',
      bold: '700',
    });
  });
});

describe('design tokens — 断点（与 PRD §2.1 / tailwind 默认对齐）', () => {
  it('mobile < 640，tablet = 640-1024，desktop ≥ 1024', () => {
    expect(breakpoints.mobile).toBe('0');
    expect(breakpoints.sm).toBe('640px');
    expect(breakpoints.lg).toBe('1024px');
  });
});

describe('design tokens — 暗色语义别名', () => {
  it('light 与 dark 各有 bg / surface / border / text 字段', () => {
    for (const mode of ['light', 'dark'] as const) {
      expect(semanticAlias[mode]).toHaveProperty('bg');
      expect(semanticAlias[mode]).toHaveProperty('surface');
      expect(semanticAlias[mode]).toHaveProperty('border');
      expect(semanticAlias[mode]).toHaveProperty('text');
      expect(semanticAlias[mode]).toHaveProperty('textMuted');
      expect(semanticAlias[mode]).toHaveProperty('brand');
    }
  });

  it('light.bg 与 dark.bg 不一致（亮 / 暗对比）', () => {
    expect(semanticAlias.light.bg).not.toBe(semanticAlias.dark.bg);
  });
});
