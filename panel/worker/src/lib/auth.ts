import type { Env } from "../types";
import { error } from "./http";

const WINDOW_MS = 1000;
const MAX_RPS = 10;
const buckets = new Map<string, { count: number; windowStart: number }>();

export function requireActorEmail(request: Request): string | Response {
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
    return null;
  }
  if (current.count >= MAX_RPS) {
    return error(429, { error: "rate_limited", detail: "too many requests" });
  }
  current.count += 1;
  return null;
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
