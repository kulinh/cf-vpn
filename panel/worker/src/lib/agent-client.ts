import type { Env } from "../types";
import { serviceTokenHeaders } from "./auth";
import { validateAdminHost } from "./hosts";

const DEFAULT_TIMEOUT_MS = 8000;

async function withTimeout(input: RequestInfo, init: RequestInit, timeoutMs: number): Promise<Response> {
  const controller = new AbortController();
  const timer = setTimeout(() => controller.abort(), timeoutMs);
  try {
    return await fetch(input, { ...init, signal: controller.signal });
  } finally {
    clearTimeout(timer);
  }
}

export async function callAgent<T>(
  env: Env,
  adminHost: string,
  path: string,
  init: RequestInit = {},
  timeoutMs = DEFAULT_TIMEOUT_MS
): Promise<T> {
  const hostError = validateAdminHost(adminHost, env);
  if (hostError) {
    throw new Error(`invalid_admin_host: ${hostError}`);
  }

  const headers: HeadersInit = {
    "content-type": "application/json",
    ...serviceTokenHeaders(env),
    ...(init.headers ?? {})
  };
  const response = await withTimeout(`https://${adminHost}${path}`, { ...init, headers }, timeoutMs);
  const payload = (await response.json().catch(() => null)) as { error?: string; detail?: string } | null;
  if (!response.ok) {
    throw new Error(payload?.detail || payload?.error || `agent_http_${response.status}`);
  }
  return payload as T;
}
