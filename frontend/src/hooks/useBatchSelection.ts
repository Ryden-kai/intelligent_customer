// useBatchSelection — generic multi-row selection state for admin
// tables with checkbox columns.
//
// Behaviour:
//   - toggle(id) flips a single row.
//   - selectAll(ids) replaces the selection with the given id list
//     (usually the visible page's ids).
//   - clear() empties the selection.
//   - isSelected(id) / count / ids are memoised for stable identity
//     in table renders.
//   - All operations are O(1) on average (Set-backed).

import { useCallback, useMemo, useState } from 'react';

export interface BatchSelectionApi {
  selected: Set<string>;
  count: number;
  ids: string[];
  toggle: (id: string) => void;
  selectAll: (ids: string[]) => void;
  clear: () => void;
  isSelected: (id: string) => boolean;
}

export function useBatchSelection(initial: string[] = []): BatchSelectionApi {
  const [selected, setSelected] = useState<Set<string>>(() => new Set(initial));

  const toggle = useCallback((id: string) => {
    setSelected((prev) => {
      const next = new Set(prev);
      if (next.has(id)) {
        next.delete(id);
      } else {
        next.add(id);
      }
      return next;
    });
  }, []);

  const selectAll = useCallback((ids: string[]) => {
    setSelected(() => new Set(ids));
  }, []);

  const clear = useCallback(() => {
    setSelected(() => new Set());
  }, []);

  const isSelected = useCallback(
    (id: string) => selected.has(id),
    [selected],
  );

  return useMemo(
    () => ({
      selected,
      count: selected.size,
      ids: Array.from(selected),
      toggle,
      selectAll,
      clear,
      isSelected,
    }),
    [selected, toggle, selectAll, clear, isSelected],
  );
}