// ChartContainer — recharts wrapper that applies our design tokens for
// the admin pages. Centralises the dark-aware colour scheme so every
// chart looks consistent across the admin.

import { PropsWithChildren } from 'react';
import { ResponsiveContainer } from 'recharts';

export interface ChartContainerProps {
  height?: number;
  title?: string;
  className?: string;
}

/** Default colour palette (light + dark via tailwind dark: classes). */
export const CHART_COLORS = {
  primary: '#0284c7',
  secondary: '#16a34a',
  warning: '#f59e0b',
  danger: '#dc2626',
  info: '#6366f1',
  pink: '#ec4899',
  emerald: '#10b981',
  amber: '#f59e0b',
  violet: '#8b5cf6',
  cyan: '#06b6d4',
};

export function ChartContainer({
  height = 240,
  title,
  className,
  children,
}: PropsWithChildren<ChartContainerProps>) {
  return (
    <div
      className={`bg-app-surface border border-app-border rounded-xl p-4 ${className || ''}`}
    >
      {title && <div className="text-sm text-app-text-muted mb-2">{title}</div>}
      <div style={{ width: '100%', height }}>
        <ResponsiveContainer width="100%" height="100%">
          {children as any}
        </ResponsiveContainer>
      </div>
    </div>
  );
}

export default ChartContainer;