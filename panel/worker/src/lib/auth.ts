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

export function requireActorEmail(request: Request, env: Env): string | Response {
  // The `Cf-Access-Authenticated-User-Email` header is only authoritative
  // when Cloudflare Access fronts the Worker. Access also sets
  // `CF-Access-Jwt-Assertion`; if that header is absent the request bypassed
  // Access (e.g. a *.workers.dev URL), so we must reject — the email header
  // would otherwise be forgeable. (Presence is the cheap guard.)
  //
  // NOTE: there is no `workers_dev = false` in wrangler.toml — workers.dev is
  // deliberately ENABLED there, because the Telegram webhook is registered
  // against it. The only actual defence against the workers.dev bypass is the
  // hostname check at the top of src/index.ts, which 404s every path but
  // /telegram/webhook on a *.workers.dev request. Do not weaken that check
  // believing a wrangler setting is backing it up.
  //
  // NOTE: Full JWT signature verification against the team's JWKS
  // (gated on optional env.ACCESS_TEAM_DOMAIN + env.ACCESS_AUD) is intentionally
  // NOT implemented here: it requires the `jose` library, which is not present
  // in node_modules, and the task forbids adding a new dependency. When `jose`
  // is added, verify the JWT here whenever both env vars are set, and keep this
  // presence-check as the fallback when they are not (so the panel never locks).
  const jwt = request.headers.get("CF-Access-Jwt-Assertion")?.trim();
  if (!jwt) {
    // Access is not in front of this request. Fall back to basic auth when it
    // is configured; when it is not, fail closed — an unauthenticated /api/*
    // exposes agent secrets, obfs passwords and every user's sub_token.
    const basic = checkBasicAuth(request, env);
    if (basic !== null) return basic;
    return error(401, { error: "unauthorized", detail: "missing access jwt" });
  }
  const email = request.headers.get("Cf-Access-Authenticated-User-Email")?.trim();
  if (!email) {
    return error(401, { error: "unauthorized", detail: "missing access email" });
  }
  return email;
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
