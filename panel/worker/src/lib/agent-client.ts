import type { Env } from "../types";
import { serviceTokenHeaders } from "./auth";
import { validateAdminHost } from "./hosts";

const DEFAULT_TIMEOUT_MS = 8000;
// Cloudflare terminates a Worker subrequest with a 524 at ~100s, so any budget
// beyond that is unobservable. Cap every caller below it so the Worker returns
// its own error (and frees the isolate) instead of being cut off mid-flight.
export const MAX_TIMEOUT_MS = 55000;

// Thrown for a non-2xx agent response. The status is carried structurally so
// callers can distinguish a config error (4xx: bad agent_secret, validation)
// from a transport failure without regex-matching a message the agent controls.
export class AgentHttpError extends Error {
  constructor(readonly status: number, message: string) {
    super(message);
    this.name = "AgentHttpError";
  }
}

// Errors raised before/instead of an HTTP round-trip that likewise mean
// "config problem", not "node down".
const CONFIG_ERROR_RE = /^(invalid_admin_host|agent_auth_missing)/;

export function isConfigError(e: unknown): boolean {
  if (e instanceof AgentHttpError) {
    return e.status >= 400 && e.status < 500;
  }
  return CONFIG_ERROR_RE.test(e instanceof Error ? e.message : String(e));
}

// True when the call ran out of budget with no answer from the agent — either
// the fetch itself was aborted or the body read was (agent_body_timeout below).
// Callers that mutate node state must treat this as "unknown", never as
// "failed": the agent may have completed the operation regardless.
export function isTimeoutError(e: unknown): boolean {
  if (e instanceof AgentHttpError) {
    return false;
  }
  const name = typeof e === "object" && e !== null ? String((e as { name?: unknown }).name ?? "") : "";
  if (name === "AbortError" || name === "TimeoutError") {
    return true;
  }
  const msg = e instanceof Error ? e.message : String(e);
  return /agent_body_timeout|AbortError|TimeoutError|aborted|timed out/i.test(msg);
}

async function fetchJsonWithTimeout(
  input: RequestInfo,
  init: RequestInit,
  timeoutMs: number
): Promise<{ response: Response; payload: { error?: string; detail?: string } | null }> {
  const controller = new AbortController();
  const timer = setTimeout(() => controller.abort(), timeoutMs);
  try {
    const response = await fetch(input, { ...init, signal: controller.signal });
    // fetch() resolves as soon as the headers arrive. Read the body inside the
    // same deadline (the abort signal is still live) — otherwise a node that
    // sends 200 + headers and then stalls pins the request until Cloudflare's
    // own 524, or until the scheduled handler's wall clock, every 5 minutes.
    let payload: { error?: string; detail?: string } | null = null;
    try {
      payload = (await response.json()) as { error?: string; detail?: string } | null;
    } catch (e) {
      if (controller.signal.aborted) {
        // Deadline hit while draining the body — a stall, not a parse failure.
        throw new Error(`agent_body_timeout: response body not read within ${timeoutMs}ms`);
      }
      // A non-JSON body is tolerated; the status alone decides ok/error.
      payload = null;
    }
    return { response, payload };
  } finally {
    clearTimeout(timer);
  }
}

export interface AgentTarget {
  adminHost: string;
  // agentSecret is the per-node bearer mirrored from /etc/cfvpn/cfvpn.env into
  // nodes.agent_secret in D1. When empty the Worker falls back to env.AGENT_SHARED_SECRET
  // (legacy single-secret mode); new deployments must always populate it.
  agentSecret?: string | null;
  // Optional node id, used only to make the "no per-node secret" warning
  // actionable.
  nodeId?: string;
}

export async function callAgent<T>(
  env: Env,
  target: AgentTarget | string,
  path: string,
  init: RequestInit = {},
  timeoutMs = DEFAULT_TIMEOUT_MS
): Promise<T> {
  const adminHost = typeof target === "string" ? target : target.adminHost;
  const perNodeSecret = typeof target === "string" ? "" : (target.agentSecret ?? "");
  const hostError = validateAdminHost(adminHost, env);
  if (hostError) {
    throw new Error(`invalid_admin_host: ${hostError}`);
  }

  const headers: HeadersInit = {
    "content-type": "application/json",
    ...serviceTokenHeaders(env),
    ...(init.headers ?? {})
  };
  let bearer = perNodeSecret;
  if (!bearer && env.AGENT_SHARED_SECRET) {
    // Legacy fleet-wide fallback. It is a secret-harvesting primitive (a node
    // row without agent_secret makes the Worker send the fleet secret to that
    // admin_host), but it cannot be removed until every prod node has
    // agent_secret populated — so make its use loud instead of silent.
    console.warn("agent_secret missing for node", typeof target === "string" ? adminHost : target.nodeId ?? adminHost);
    bearer = env.AGENT_SHARED_SECRET;
  }
  if (!bearer) {
    // Fail fast instead of silently issuing an unauthenticated request the agent
    // will reject anyway — an empty bearer means neither nodes.agent_secret nor
    // env.AGENT_SHARED_SECRET is configured, which is a misconfiguration.
    throw new Error(`agent_auth_missing: no agent_secret for ${adminHost} and AGENT_SHARED_SECRET unset`);
  }
  (headers as Record<string, string>)["Authorization"] = `Bearer ${bearer}`;
  const { response, payload } = await fetchJsonWithTimeout(
    `https://${adminHost}${path}`,
    { ...init, headers },
    Math.min(timeoutMs, MAX_TIMEOUT_MS)
  );
  if (!response.ok) {
    // Keep the `agent_http_<status>` prefix: the agent returns a JSON body for
    // most 4xx, and the old code surfaced only that body's text, so callers
    // classifying by message never saw the status at all.
    throw new AgentHttpError(
      response.status,
      `agent_http_${response.status}: ${payload?.detail || payload?.error || ""}`
    );
  }
  return payload as T;
}
