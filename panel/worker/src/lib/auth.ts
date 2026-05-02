import type { Env } from "../types";
import { error } from "./http";

const WINDOW_MS = 1000;
const MAX_RPS = 10;
const MAX_BUCKETS = 500;
const buckets = new Map<string, { count: number; windowStart: number }>();

export function requireActorEmail(request: Request): string | Response {
  // The `Cf-Access-Authenticated-User-Email` header is only authoritative
  // when Cloudflare Access fronts the Worker. Access also sets
  // `CF-Access-Jwt-Assertion`; if that header is absent the request bypassed
  // Access (e.g. a *.workers.dev URL), so we must reject — the email header
  // would otherwise be forgeable. (Full JWT signature verification against
  // the team's JWKS is out of scope here; presence is the cheap guard.)
  const jwt = request.headers.get("CF-Access-Jwt-Assertion")?.trim();
  if (!jwt) {
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
