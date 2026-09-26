// ChartContainer component tests.

import { describe, expect, it } from 'vitest';
import { render, screen } from '@testing-library/react';
import { ChartContainer, CHART_COLORS } from './ChartContainer';
import { BarChart, Bar } from 'recharts';

describe('ChartContainer', () => {
  it('renders the title', () => {
    render(
      <ChartContainer title="模板热度">
        <BarChart data={[{ name: 'a', count: 1 }]}>
          <Bar dataKey="count" />
        </BarChart>
      </ChartContainer>,
    );
    expect(screen.getByText('模板热度')).toBeInTheDocument();
  });

  it('renders without a title', () => {
    render(
      <ChartContainer>
        <BarChart data={[{ name: 'a', count: 1 }]}>
          <Bar dataKey="count" />
        </BarChart>
      </ChartContainer>,
    );
    // no title node should be rendered
    expect(screen.queryByText('模板热度')).toBeNull();
  });

  it('exposes CHART_COLORS palette', () => {
    expect(CHART_COLORS.primary).toMatch(/^#[0-9a-f]{6}$/i);
    expect(CHART_COLORS.secondary).toMatch(/^#[0-9a-f]{6}$/i);
    expect(CHART_COLORS.warning).toMatch(/^#[0-9a-f]{6}$/i);
  });
});