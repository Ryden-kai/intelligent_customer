/**
 * v2.2 PR3 — Skeleton 组件。
 *
 * 三形态：
 *   - text：水平条（默认 100% width × 0.875rem 高）
 *   - circle：圆形（适合头像 / icon 占位）
 *   - rect：矩形（适合卡片 / 图片）
 *
 * 设计要点：
 *   - 默认启用 shimmer 动画（通过 .ic-skeleton-shimmer class）
 *   - 颜色跟随语义 surface-muted（dark mode 自动适配）
 *   - role="status" + aria-label：让屏幕阅读器知道"加载中"
 *   - width / height 接受 number（px）或 string（如 "100%" / "1rem"）
 *
 * 用法：
 *   <Skeleton variant="text" />
 *   <Skeleton variant="circle" width={40} height={40} />
 *   <Skeleton variant="rect" height={120} />
 *   <Skeleton variant="text" count={3} />  // 3 行
 */

import { useMemo } from 'react';

export type SkeletonVariant = 'text' | 'circle' | 'rect';

export interface SkeletonProps {
  /** 形态（默认 text） */
  variant?: SkeletonVariant;
  /** 宽度（px 数字或带单位字符串） */
  width?: number | string;
  /** 高度（px 数字或带单位字符串） */
  height?: number | string;
  /** 是否启用 shimmer 动画（默认 true） */
  animate?: boolean;
  /** 重复次数（仅 text 形态生效，简化列表占位） */
  count?: number;
  /** 自定义 className */
  className?: string;
  /** a11y：aria-label，默认"加载中" */
  ariaLabel?: string;
}

function toSize(v: number | string | undefined, fallback: string): string {
  if (v === undefined) return fallback;
  return typeof v === 'number' ? `${v}px` : v;
}

function SkeletonItem({
  variant = 'text',
  width,
  height,
  animate = true,
  className = '',
  ariaLabel = '加载中',
}: Omit<SkeletonProps, 'count'>) {
  const style = useMemo<React.CSSProperties>(() => {
    if (variant === 'circle') {
      const size = toSize(width ?? height, '2.5rem');
      return { width: size, height: size };
    }
    if (variant === 'rect') {
      return { width: toSize(width, '100%'), height: toSize(height, '6rem') };
    }
    // text
    return {
      width: toSize(width, '100%'),
      height: toSize(height, '0.875rem'),
    };
  }, [variant, width, height]);

  const baseCls = `inline-block bg-app-surface-muted ${
    variant === 'circle' ? 'rounded-full' : 'rounded-md'
  } ${animate ? 'ic-skeleton-shimmer' : ''} ${className}`;

  return (
    <span
      role="status"
      aria-label={ariaLabel}
      aria-busy="true"
      style={style}
      className={baseCls}
      data-testid="skeleton-item"
    />
  );
}

export function Skeleton(props: SkeletonProps) {
  const { count = 1, variant = 'text', ...rest } = props;
  if (variant === 'text' && count > 1) {
    return (
      <div className="flex flex-col gap-2" data-testid="skeleton">
        {Array.from({ length: count }).map((_, i) => (
          <SkeletonItem key={i} {...rest} variant="text" ariaLabel={rest.ariaLabel} />
        ))}
      </div>
    );
  }
  return (
    <SkeletonItem {...rest} variant={variant} ariaLabel={rest.ariaLabel} data-testid="skeleton" />
  );
}

export default Skeleton;
