// FilterPanel component tests.

import { describe, expect, it, vi } from 'vitest';
import { render, screen, fireEvent } from '@testing-library/react';
import { FilterPanel } from './FilterPanel';

interface F {
  from: string;
  to: string;
  status: string;
}

const FIELDS = [
  { id: 'from', label: '起始时间', type: 'datetime' as const },
  { id: 'to', label: '结束时间', type: 'datetime' as const },
  { id: 'status', label: '状态', type: 'select' as const, options: [
    { value: 'open', label: '进行中' },
    { value: 'closed', label: '已关闭' },
  ] },
];

const EMPTY: F = { from: '', to: '', status: '' };

describe('FilterPanel', () => {
  it('renders all configured labels', () => {
    render(<FilterPanel<F> filters={EMPTY} fields={FIELDS} onApply={() => {}} onReset={() => {}} />);
    expect(screen.getByText('起始时间')).toBeInTheDocument();
    expect(screen.getByText('结束时间')).toBeInTheDocument();
    expect(screen.getByText('状态')).toBeInTheDocument();
  });

  it('renders select options', () => {
    render(<FilterPanel<F> filters={EMPTY} fields={FIELDS} onApply={() => {}} onReset={() => {}} />);
    expect(screen.getByText('全部')).toBeInTheDocument();
    expect(screen.getByText('进行中')).toBeInTheDocument();
    expect(screen.getByText('已关闭')).toBeInTheDocument();
  });

  it('calls onApply with the current draft on 搜索 click', () => {
    const onApply = vi.fn();
    render(<FilterPanel<F> filters={EMPTY} fields={FIELDS} onApply={onApply} onReset={() => {}} />);
    fireEvent.change(screen.getByLabelText('状态'), { target: { value: 'closed' } });
    fireEvent.click(screen.getByText('🔍 搜索'));
    expect(onApply).toHaveBeenCalledWith(expect.objectContaining({ status: 'closed' }));
  });

  it('calls onReset on 重置 click', () => {
    const onReset = vi.fn();
    render(<FilterPanel<F> filters={EMPTY} fields={FIELDS} onApply={() => {}} onReset={onReset} />);
    fireEvent.click(screen.getByText('↺ 重置'));
    expect(onReset).toHaveBeenCalled();
  });

  it('updates datetime inputs', () => {
    const onApply = vi.fn();
    render(<FilterPanel<F> filters={EMPTY} fields={FIELDS} onApply={onApply} onReset={() => {}} />);
    fireEvent.change(screen.getByLabelText('起始时间'), { target: { value: '2026-09-25T00:00' } });
    fireEvent.click(screen.getByText('🔍 搜索'));
    expect(onApply).toHaveBeenCalledWith(expect.objectContaining({ from: '2026-09-25T00:00' }));
  });

  it('renders text fields with placeholder', () => {
    const fieldsWithText = [
      ...FIELDS,
      { id: 'keyword', label: '关键词', type: 'text' as const, placeholder: '搜索...' },
    ];
    render(
      <FilterPanel filters={EMPTY} fields={fieldsWithText} onApply={() => {}} onReset={() => {}} />,
    );
    const input = screen.getByLabelText('关键词') as HTMLInputElement;
    expect(input.placeholder).toBe('搜索...');
  });

  it('renders number fields', () => {
    const fieldsWithNumber = [
      { id: 'score', label: '分数', type: 'number' as const, min: 0, max: 100 },
    ];
    render(
      <FilterPanel filters={{ score: '' }} fields={fieldsWithNumber} onApply={() => {}} onReset={() => {}} />,
    );
    const input = screen.getByLabelText('分数') as HTMLInputElement;
    expect(input.type).toBe('number');
    expect(input.min).toBe('0');
    expect(input.max).toBe('100');
  });
});