// scripts/sign.js — minimal Node helper for hitting the API with valid
// HMAC headers. Useful for ad-hoc curl-style smoke tests when the
// frontend dev server is not yet wired up.
//
// Usage:
//   node scripts/sign.js POST /api/chat '{"content":"hi"}'
//
// Reads SECRET from JWT_SECRET in .env (or env var).

const crypto = require('node:crypto');
const fs = require('node:fs');

function loadSecret() {
  if (process.env.JWT_SECRET && process.env.JWT_SECRET.length >= 16) return process.env.JWT_SECRET;
  const env = fs.readFileSync('.env', 'utf8');
  const m = env.match(/^JWT_SECRET=(.+)$/m);
  if (!m) throw new Error('JWT_SECRET not set');
  return m[1].trim();
}

const [method = 'POST', path = '/api/chat', body = '{}'] = process.argv.slice(2);
const secret = loadSecret();
const ts = Date.now();
const nonce = crypto.randomBytes(16).toString('hex');
const bodyHash = crypto.createHash('sha256').update(body).digest('hex');
const canonical = [method.toUpperCase(), path, String(ts), nonce, bodyHash].join('\n');
const sig = crypto.createHmac('sha256', secret).update(canonical).digest('hex');

process.stdout.write(JSON.stringify({
  headers: {
    'X-Timestamp': String(ts),
    'X-Nonce': nonce,
    'X-Signature': sig,
    'Content-Type': 'application/json',
  },
  body,
  method,
  path,
}));
