// useExportCsv — fetches a CSV endpoint with bearer auth and triggers
// a download. Used by the admin pages for the "导出 CSV" button.
//
// Implementation notes:
//   - We use fetch + blob (not <a href download>) so 401/403 responses
//     can be surfaced to the user instead of silently producing an
//     empty file.
//   - UTF-8 BOM is preserved by the browser blob pipeline — the server
//     already writes \xEF\xBB\xBF at the start of the CSV body.
//   - The default filename uses a `prefix_YYYY-MM-DD.csv` shape so
//     multiple downloads in one session don't collide.

import { useCallback, useState } from 'react';

export interface ExportCsvOptions {
  /** Bearer token. Omit for unauthenticated requests. */
  token?: string | null;
  /** Filename prefix (default: "export"). */
  filenamePrefix?: string;
  /** Extra query params (object form). */
  params?: Record<string, string | number | boolean | undefined | null>;
}

export interface ExportCsvApi {
  loading: boolean;
  error: string | null;
  trigger: (url: string, opts?: ExportCsvOptions) => Promise<void>;
}

export function useExportCsv(): ExportCsvApi {
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const trigger = useCallback(
    async (url: string, opts: ExportCsvOptions = {}) => {
      setLoading(true);
      setError(null);
      try {
        // Compose URL with both filter params and token (EventSource-style).
        const sp = new URLSearchParams();
        const { params, token, filenamePrefix } = opts;
        if (params) {
          for (const [k, v] of Object.entries(params)) {
            if (v === undefined || v === null || v === '') continue;
            sp.set(k, String(v));
          }
        }
        if (token) sp.set('t', token);
        const full = sp.toString() ? `${url}?${sp.toString()}` : url;

        const headers: Record<string, string> = {};
        if (token) headers.Authorization = `Bearer ${token}`;

        const res = await fetch(full, { headers });
        if (!res.ok) {
          let msg = `HTTP ${res.status}`;
          try {
            const j = await res.json();
            if (j?.message) msg = j.message;
          } catch {
            /* ignore */
          }
          throw new Error(msg);
        }
        const blob = await res.blob();
        const filename =
          (filenamePrefix || 'export') +
          '_' +
          new Date().toISOString().slice(0, 10) +
          '.csv';
        const a = document.createElement('a');
        const objUrl = URL.createObjectURL(blob);
        a.href = objUrl;
        a.download = filename;
        a.click();
        URL.revokeObjectURL(objUrl);
      } catch (e: any) {
        setError(e?.message ?? String(e));
      } finally {
        setLoading(false);
      }
    },
    [],
  );

  return { loading, error, trigger };
}