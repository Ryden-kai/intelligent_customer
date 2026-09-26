// useExportCsv hook tests.

import { describe, expect, it, beforeEach, afterEach, vi } from 'vitest';
import { act, renderHook } from '@testing-library/react';
import { useExportCsv } from './useExportCsv';

type AnchorEl = {
  href: string;
  download: string;
  click: () => void;
};

let captured: AnchorEl[] = [];

beforeEach(() => {
  captured = [];
  // jsdom doesn't implement URL.createObjectURL cleanly for our test;
  // provide a stub that returns a string we can inspect.
  (globalThis as any).URL.createObjectURL = vi.fn(() => 'blob:test');
  (globalThis as any).URL.revokeObjectURL = vi.fn();

  (globalThis as any).fetch = vi.fn().mockResolvedValue({
    ok: true,
    blob: async () => new Blob(['id,name\n1,a\n'], { type: 'text/csv' }),
  });

  // Track <a> clicks so we can assert the download was triggered.
  const realCreate = document.createElement.bind(document);
  vi.spyOn(document, 'createElement').mockImplementation((tag: string) => {
    const el = realCreate(tag) as any;
    if (tag === 'a') {
      captured.push(el);
      el.click = vi.fn();
    }
    return el;
  });
});

afterEach(() => {
  vi.restoreAllMocks();
  delete (globalThis as any).fetch;
});

describe('useExportCsv', () => {
  it('downloads via blob + click', async () => {
    const { result } = renderHook(() => useExportCsv());
    await act(async () => {
      await result.current.trigger('/api/x.csv');
    });
    expect(captured).toHaveLength(1);
    expect(captured[0].download).toMatch(/^export_\d{4}-\d{2}-\d{2}\.csv$/);
    expect(captured[0].click).toHaveBeenCalled();
    expect(result.current.loading).toBe(false);
    expect(result.current.error).toBeNull();
  });

  it('prefixes filename with custom value', async () => {
    const { result } = renderHook(() => useExportCsv());
    await act(async () => {
      await result.current.trigger('/api/x.csv', { filenamePrefix: 'jev_decisions' });
    });
    expect(captured[0].download.startsWith('jev_decisions_')).toBe(true);
  });

  it('attaches token to Authorization header', async () => {
    const fetchMock = (globalThis as any).fetch as ReturnType<typeof vi.fn>;
    const { result } = renderHook(() => useExportCsv());
    await act(async () => {
      await result.current.trigger('/api/x.csv', { token: 'abc123' });
    });
    expect(fetchMock).toHaveBeenCalledWith(
      expect.stringContaining('t=abc123'),
      expect.objectContaining({ headers: expect.objectContaining({ Authorization: 'Bearer abc123' }) }),
    );
  });

  it('records error on non-2xx response', async () => {
    (globalThis as any).fetch = vi.fn().mockResolvedValue({
      ok: false,
      status: 403,
      json: async () => ({ message: 'forbidden' }),
    });
    const { result } = renderHook(() => useExportCsv());
    await act(async () => {
      await result.current.trigger('/api/x.csv');
    });
    expect(result.current.error).toContain('forbidden');
    expect(captured).toHaveLength(0);
  });

  it('skips empty params', async () => {
    const fetchMock = (globalThis as any).fetch as ReturnType<typeof vi.fn>;
    const { result } = renderHook(() => useExportCsv());
    await act(async () => {
      await result.current.trigger('/api/x.csv', {
        params: { foo: 'bar', empty: '', gone: undefined },
      });
    });
    const url = fetchMock.mock.calls[0][0];
    expect(url).toContain('foo=bar');
    expect(url).not.toContain('empty');
    expect(url).not.toContain('gone');
  });
});