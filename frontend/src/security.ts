// Browser-side signing helpers. Mirrors the server's CanonicalString + HMAC
// math (internal/security/signing.go) so the front-end can produce the same
// X-Timestamp / X-Nonce / X-Signature headers.
//
// `VITE_API_SIGN_SECRET` MUST match the backend's JWT_SECRET (when the
// backend is using the implicit HKDF-derived signing key). The frontend
// bundle is public, so this shared secret is only safe when:
//   1. the SPA is hosted on a trusted origin (e.g. your own domain), and
//   2. HTTPS is enforced end-to-end so the headers can't be sniffed
// In production rotate the secret via env + redeploy both ends together.

// Dev-only fallback. MUST match the backend's default JWT_SECRET (see
// backend/.env.example) so `npm run dev` works out of the box without
// copying .env.example → .env. In production builds (import.meta.env.DEV
// === false), the code below will throw if VITE_API_SIGN_SECRET is not
// set, forcing the operator to configure the real production secret.
const DEFAULT_DEV_SIGN_SECRET = 'dev-secret-please-change-in-production-must-be-32b';

const enc = new TextEncoder();

function toHex(bytes: ArrayBuffer | Uint8Array): string {
  const arr = bytes instanceof Uint8Array ? bytes : new Uint8Array(bytes);
  return Array.from(arr, (b) => b.toString(16).padStart(2, '0')).join('');
}

async function sha256Hex(text: string): Promise<string> {
  const buf = await crypto.subtle.digest('SHA-256', enc.encode(text));
  return toHex(buf);
}

async function hmacHex(secret: string, text: string): Promise<string> {
  const keyData = enc.encode(secret);
  const key = await crypto.subtle.importKey(
    'raw',
    keyData,
    { name: 'HMAC', hash: 'SHA-256' },
    false,
    ['sign']
  );
  const sig = await crypto.subtle.sign('HMAC', key, enc.encode(text));
  return toHex(sig);
}

function nonce(): string {
  // 32-char hex nonce (matches server's security.RandomNonce).
  const buf = new Uint8Array(16);
  crypto.getRandomValues(buf);
  return toHex(buf);
}

function canonicalString(method: string, path: string, ts: number, n: string, bodyHashHex: string): string {
  return [method.toUpperCase(), path, String(ts), n, bodyHashHex].join('\n');
}

export interface SignedHeaders {
  'X-Timestamp': string;
  'X-Nonce': string;
  'X-Signature': string;
}

export async function sign(method: string, url: string, body: string): Promise<SignedHeaders> {
  const envSecret = (import.meta.env.VITE_API_SIGN_SECRET as string | undefined)?.trim();
  if (!envSecret && !import.meta.env.DEV) {
    throw new Error(
      'VITE_API_SIGN_SECRET is required in production builds. Set it in frontend/.env before bundling.',
    );
  }
  const secret = envSecret || DEFAULT_DEV_SIGN_SECRET;
  const u = new URL(url, window.location.origin);
  const path = u.pathname + (u.search ? u.search : '');
  const ts = Date.now();
  const n = nonce();
  const bodyHashHex = await sha256Hex(body ?? '');
  const canonical = canonicalString(method, path, ts, n, bodyHashHex);
  const signature = await hmacHex(secret, canonical);
  return {
    'X-Timestamp': String(ts),
    'X-Nonce': n,
    'X-Signature': signature,
  };
}
