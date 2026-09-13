import type { Env } from "../types";
import { error } from "./http";

const WINDOW_MS = 1000;
const MAX_RPS = 10;
const MAX_BUCKETS = 500;
// Best-effort rate limiting: this Map is scoped to a single Worker isolate and
// resets when the isolate is evicted. It won't stop a distributed burst across
// multiple isolates. For hard enforcement, configure Cloudflare Zone-level rate
// limiting rules in the dashboard (Workers > Rate Limiting).
const buckets = new Map<string, { count: number; windowStart: number }>();

/**
 * Constant-time string comparison.
 *
 * `===` on a secret leaks its length and its matching prefix through timing.
 * The cost here is nothing and the alternative is a credential check that
 * answers "how much of this password is right".
 */
function timingSafeEqual(a: string, b: string): boolean {
  if (a.length !== b.length) return false;
  let diff = 0;
  for (let i = 0; i < a.length; i += 1) {
    diff |= a.charCodeAt(i) ^ b.charCodeAt(i);
  }
  return diff === 0;
}

/**
 * checkBasicAuth authenticates against PANEL_BASIC_USER / PANEL_BASIC_PASS.
 *
 * Both must be set. A half-configured pair returns null rather than matching
 * an empty string, so a missing secret can never become "any password works".
 * Returns the username as the actor on success, null when basic auth is not
 * configured, and a 401 carrying WWW-Authenticate when it is configured and
 * the credentials do not match — without that header the browser never prompts.
 */
function checkBasicAuth(request: Request, env: Env): string | Response | null {
  const user = env.PANEL_BASIC_USER?.trim();
  const pass = env.PANEL_BASIC_PASS;
  if (!user || !pass) return null;

  const unauthorized = () =>
    new Response(JSON.stringify({ error: "unauthorized", detail: "basic auth required" }), {
      status: 401,
      headers: {
        "content-type": "application/json",
        "WWW-Authenticate": 'Basic realm="cf-vpn panel", charset="UTF-8"'
      }
    });

  const header = request.headers.get("Authorization") ?? "";
  if (!header.startsWith("Basic ")) return unauthorized();
  let decoded: string;
  try {
    decoded = atob(header.slice(6).trim());
  } catch {
    return unauthorized();
  }
  const sep = decoded.indexOf(":");
  if (sep < 0) return unauthorized();
  const gotUser = decoded.slice(0, sep);
  const gotPass = decoded.slice(sep + 1);
  if (timingSafeEqual(gotUser, user) && timingSafeEqual(gotPass, pass)) {
    return gotUser;
  }
  return unauthorized();
}

// requireActorEmail authenticates an /api/* request and returns the actor.
//
// Cloudflare Access headers are client-controlled unless Access actually sits
// in front of the Worker, and nothing in the request proves that. So they are
// trusted ONLY when ACCESS_TEAM_DOMAIN and ACCESS_AUD are configured AND the
// CF-Access-Jwt-Assertion verifies (RS256 against the team JWKS, aud, iss,
// exp); the actor is the email inside the verified token, never the
// Cf-Access-Authenticated-User-Email header. Without that configuration the
// Access headers are ignored and basic auth is required. Checking only that
// the JWT header was present (what this did before 2026-09-13) let anyone
// who sent two made-up headers in as admin once Access was taken off.
//
// NOTE: there is no `workers_dev = false` in wrangler.toml — workers.dev is
// deliberately ENABLED there, because the Telegram webhook is registered
// against it. The only actual defence against the workers.dev bypass is the
// hostname check at the top of src/index.ts, which 404s every path but
// /telegram/webhook on a *.workers.dev request. Do not weaken that check
// believing a wrangler setting is backing it up.
export async function requireActorEmail(request: Request, env: Env, fetcher: typeof fetch = fetch): Promise<string | Response> {
  const jwt = request.headers.get("CF-Access-Jwt-Assertion")?.trim();
  if (jwt && accessConfigured(env)) {
    const email = await verifyAccessJwt(jwt, env, fetcher);
    if (email) return email;
    return error(401, { error: "unauthorized", detail: "invalid access jwt" });
  }
  // Access is not configured (or not in front of this request): fall back to
  // basic auth when it is configured; when it is not, fail closed — an
  // unauthenticated /api/* exposes agent secrets, obfs passwords and every
  // user's sub_token.
  const basic = checkBasicAuth(request, env);
  if (basic !== null) return basic;
  return error(401, { error: "unauthorized", detail: "no authentication configured" });
}

function accessConfigured(env: Env): boolean {
  return !!env.ACCESS_TEAM_DOMAIN?.trim() && !!env.ACCESS_AUD?.trim();
}

// "rwl265.cloudflareaccess.com", with or without scheme / trailing slash.
function teamOrigin(env: Env): string {
  const host = env.ACCESS_TEAM_DOMAIN!.trim().replace(/^https?:\/\//, "").replace(/\/+$/, "");
  return `https://${host}`;
}

const JWKS_TTL_MS = 10 * 60 * 1000;
// Clock skew tolerated on exp / nbf.
const JWT_LEEWAY_S = 60;
let jwksCache: { origin: string; fetchedAt: number; keys: JsonWebKey[] } | null = null;

// Test hook: forget the cached key set.
export function resetAccessKeyCache(): void {
  jwksCache = null;
}

async function accessKeys(origin: string, fetcher: typeof fetch, refresh: boolean): Promise<JsonWebKey[]> {
  const now = Date.now();
  if (!refresh && jwksCache && jwksCache.origin === origin && now - jwksCache.fetchedAt < JWKS_TTL_MS) {
    return jwksCache.keys;
  }
  const res = await fetcher(`${origin}/cdn-cgi/access/certs`, { signal: AbortSignal.timeout(5000) });
  if (!res.ok) throw new Error(`access certs: HTTP ${res.status}`);
  const body = await res.json() as { keys?: JsonWebKey[] };
  const keys = Array.isArray(body.keys) ? body.keys : [];
  jwksCache = { origin, fetchedAt: now, keys };
  return keys;
}

function b64urlBytes(s: string): Uint8Array {
  const b64 = s.replace(/-/g, "+").replace(/_/g, "/") + "=".repeat((4 - (s.length % 4)) % 4);
  const bin = atob(b64);
  const out = new Uint8Array(bin.length);
  for (let i = 0; i < bin.length; i += 1) out[i] = bin.charCodeAt(i);
  return out;
}

function b64urlJson(s: string): Record<string, unknown> | null {
  try {
    const v = JSON.parse(new TextDecoder().decode(b64urlBytes(s)));
    return v && typeof v === "object" ? v as Record<string, unknown> : null;
  } catch {
    return null;
  }
}

// verifyAccessJwt returns the verified email, or null for anything that does
// not check out (malformed, wrong alg, unknown kid, bad signature, wrong
// aud/iss, expired, JWKS unreachable).
export async function verifyAccessJwt(jwt: string, env: Env, fetcher: typeof fetch = fetch): Promise<string | null> {
  try {
    const parts = jwt.split(".");
    if (parts.length !== 3) return null;
    const header = b64urlJson(parts[0]);
    const payload = b64urlJson(parts[1]);
    if (!header || !payload || header.alg !== "RS256" || typeof header.kid !== "string") return null;

    const origin = teamOrigin(env);
    let jwk = (await accessKeys(origin, fetcher, false)).find((k) => (k as { kid?: string }).kid === header.kid);
    if (!jwk) {
      // Access rotates its signing keys; one refetch covers a new kid.
      jwk = (await accessKeys(origin, fetcher, true)).find((k) => (k as { kid?: string }).kid === header.kid);
    }
    if (!jwk) return null;
    const key = await crypto.subtle.importKey("jwk", jwk, { name: "RSASSA-PKCS1-v1_5", hash: "SHA-256" }, false, ["verify"]);
    const ok = await crypto.subtle.verify(
      "RSASSA-PKCS1-v1_5", key, b64urlBytes(parts[2]), new TextEncoder().encode(`${parts[0]}.${parts[1]}`)
    );
    if (!ok) return null;

    const now = Math.floor(Date.now() / 1000);
    const aud = env.ACCESS_AUD!.trim();
    const auds = Array.isArray(payload.aud) ? payload.aud : [payload.aud];
    if (!auds.includes(aud)) return null;
    if (payload.iss !== origin) return null;
    if (typeof payload.exp !== "number" || payload.exp + JWT_LEEWAY_S < now) return null;
    if (typeof payload.nbf === "number" && payload.nbf - JWT_LEEWAY_S > now) return null;
    const email = typeof payload.email === "string" ? payload.email.trim() : "";
    return email || null;
  } catch {
    return null;
  }
}

export function enforceRateLimit(email: string): Response | null {
  const now = Date.now();
  const current = buckets.get(email);
  if (!current || now - current.windowStart >= WINDOW_MS) {
    buckets.set(email, { count: 1, windowStart: now });
    evictStaleBuckets(now);
    return null;
  }
  if (current.count >= MAX_RPS) {
    return error(429, { error: "rate_limited", detail: "too many requests" });
  }
  current.count += 1;
  return null;
}

function evictStaleBuckets(now: number) {
  if (buckets.size <= MAX_BUCKETS) return;
  for (const [k, v] of buckets) {
    if (now - v.windowStart >= WINDOW_MS) {
      buckets.delete(k);
    }
  }
  // Fallback: if still over limit, delete the oldest entry.
  if (buckets.size > MAX_BUCKETS) {
    let oldest = "";
    let oldestTs = Infinity;
    for (const [k, v] of buckets) {
      if (v.windowStart < oldestTs) {
        oldestTs = v.windowStart;
        oldest = k;
      }
    }
    if (oldest) buckets.delete(oldest);
  }
}

export function serviceTokenHeaders(env: Env): Record<string, string> {
  if (!env.CF_ACCESS_CLIENT_ID || !env.CF_ACCESS_CLIENT_SECRET) {
    return {};
  }
  const idHeader = env.SERVICE_TOKEN_HEADER_ID || "CF-Access-Client-Id";
  const secretHeader = env.SERVICE_TOKEN_HEADER_SECRET || "CF-Access-Client-Secret";
  return {
    [idHeader]: env.CF_ACCESS_CLIENT_ID,
    [secretHeader]: env.CF_ACCESS_CLIENT_SECRET
  };
}
