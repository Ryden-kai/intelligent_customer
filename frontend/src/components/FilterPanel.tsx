// FilterPanel — generic advanced-filter UI for the admin tables.
//
// Usage:
//   <FilterPanel
//     filters={filters}
//     fields={[
//       { id: 'from', label: '起始时间', type: 'datetime' },
//       { id: 'to', label: '结束时间', type: 'datetime' },
//       { id: 'status', label: '状态', type: 'select', options: [
//         { value: 'open', label: '进行中' },
//         { value: 'closed', label: '已关闭' },
//       ]},
//       { id: 'keyword', label: '关键词', type: 'text', placeholder: '搜索...' },
//       { id: 'score_min', label: '最低分', type: 'number' },
//     ]}
//     onApply={(next) => { setFilters(next); reload(); }}
//     onReset={() => { setFilters(EMPTY); reload(); }}
//   />
//
// The component is stateless; it owns no filter state. Parent passes the
// current filter object and an apply callback. This makes it trivial
// to serialise filters into URLs (v2.2.1) and to test in isolation.

import { useState } from 'react';

export type FilterValue = string | number | undefined;

export type FilterFieldType = 'text' | 'datetime' | 'select' | 'number';

export interface FilterFieldOption {
  value: string;
  label: string;
}

export interface FilterField {
  id: string;
  label: string;
  type: FilterFieldType;
  placeholder?: string;
  options?: FilterFieldOption[];
  min?: number;
  max?: number;
  step?: number;
}

export interface FilterPanelProps<F> {
  filters: F;
  fields: FilterField[];
  onApply: (next: F) => void;
  onReset: () => void;
  /** Compact mode (smaller inputs, single row). Default false. */
  compact?: boolean;
}

export function FilterPanel<F>(props: FilterPanelProps<F>) {
  const { filters, fields, onApply, onReset, compact } = props;
  const [draft, setDraft] = useState<F>(filters);

  function update<K extends keyof F>(k: K, v: F[K]) {
    setDraft((prev) => ({ ...prev, [k]: v }));
  }

  function apply() {
    onApply(draft);
  }

  function reset() {
    const empty = {} as F;
    setDraft(empty);
    onReset();
  }

  return (
    <div
      className="bg-app-surface border border-app-border rounded-xl p-4"
      role="region"
      aria-label="筛选面板"
    >
      <div className={`grid gap-3 ${compact ? 'grid-cols-2 md:grid-cols-3 lg:grid-cols-5' : 'grid-cols-1 md:grid-cols-2 lg:grid-cols-5'}`}>
        {fields.map((f) => (
          <div key={f.id}>
            <label
              htmlFor={`filter-${f.id}`}
              className="block text-xs text-app-text-muted mb-1"
            >
              {f.label}
            </label>
            {f.type === 'select' ? (
              <select
                id={`filter-${f.id}`}
                value={(draft[f.id as keyof F] as string) ?? ''}
                onChange={(e) => update(f.id as keyof F, e.target.value as unknown as F[keyof F])}
                className="w-full border border-app-border rounded px-2 py-1 text-sm bg-app-surface text-app-text focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-app-brand"
              >
                <option value="">全部</option>
                {(f.options || []).map((o) => (
                  <option key={o.value} value={o.value}>
                    {o.label}
                  </option>
                ))}
              </select>
            ) : (
              <input
                id={`filter-${f.id}`}
                type={f.type === 'number' ? 'number' : f.type === 'datetime' ? 'datetime-local' : 'text'}
                value={(draft[f.id as keyof F] as string | number) ?? ''}
                placeholder={f.placeholder}
                min={f.min}
                max={f.max}
                step={f.step}
                onChange={(e) => {
                  const raw = e.target.value;
                  const v =
                    f.type === 'number'
                      ? (raw === '' ? ('' as unknown as F[keyof F]) : (Number(raw) as unknown as F[keyof F]))
                      : (raw as unknown as F[keyof F]);
                  update(f.id as keyof F, v);
                }}
                className="w-full border border-app-border rounded px-2 py-1 text-sm bg-app-surface text-app-text focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-app-brand"
              />
            )}
          </div>
        ))}
      </div>
      <div className="flex gap-2 mt-3">
        <button
          type="button"
          onClick={apply}
          aria-label="应用筛选"
          className="text-xs px-3 py-1.5 rounded bg-brand-500 hover:bg-brand-600 text-white focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-brand-500"
        >
          🔍 搜索
        </button>
        <button
          type="button"
          onClick={reset}
          aria-label="重置筛选"
          className="text-xs px-3 py-1.5 rounded border border-app-border text-app-text bg-app-surface hover:bg-app-surface-muted focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-app-brand"
        >
          ↺ 重置
        </button>
      </div>
    </div>
  );
}

export default FilterPanel;