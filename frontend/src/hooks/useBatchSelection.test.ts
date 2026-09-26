// useBatchSelection hook tests.

import { describe, expect, it } from 'vitest';
import { act, renderHook } from '@testing-library/react';
import { useBatchSelection } from './useBatchSelection';

describe('useBatchSelection', () => {
  it('starts empty by default', () => {
    const { result } = renderHook(() => useBatchSelection());
    expect(result.current.count).toBe(0);
    expect(result.current.ids).toEqual([]);
  });

  it('toggle adds then removes an id', () => {
    const { result } = renderHook(() => useBatchSelection());
    act(() => result.current.toggle('a'));
    expect(result.current.count).toBe(1);
    expect(result.current.isSelected('a')).toBe(true);
    act(() => result.current.toggle('a'));
    expect(result.current.count).toBe(0);
    expect(result.current.isSelected('a')).toBe(false);
  });

  it('selectAll replaces the selection', () => {
    const { result } = renderHook(() => useBatchSelection());
    act(() => result.current.selectAll(['a', 'b', 'c']));
    expect(result.current.count).toBe(3);
    act(() => result.current.selectAll(['d']));
    expect(result.current.count).toBe(1);
    expect(result.current.ids).toEqual(['d']);
  });

  it('clear empties the selection', () => {
    const { result } = renderHook(() => useBatchSelection(['x', 'y']));
    expect(result.current.count).toBe(2);
    act(() => result.current.clear());
    expect(result.current.count).toBe(0);
  });

  it('isSelected is false for unknown ids', () => {
    const { result } = renderHook(() => useBatchSelection());
    expect(result.current.isSelected('nope')).toBe(false);
  });

  it('initial selection is honoured', () => {
    const { result } = renderHook(() => useBatchSelection(['p', 'q']));
    expect(result.current.count).toBe(2);
    expect(result.current.ids).toEqual(expect.arrayContaining(['p', 'q']));
  });
});