import type { Env } from "../types";

const CF_API_BASE = "https://api.cloudflare.com/client/v4";

// Cloudflare zone / DNS-record ids are 32 lowercase hex chars; tunnel ids are
// UUIDs (dashed, or bare hex on older records). Anything else must never reach
// a path template: fetch() normalises "../" segments, so a value like
// "../../../zones/<victim>" would retarget the request at an arbitrary endpoint
// the CF_API_TOKEN can reach (verified: DELETE .../cfd_tunnel/../../../zones/Z
// resolves to DELETE /zones/Z). tunnel_uuid arrives straight from a node agent,
// so it is attacker-controlled the moment one VPS is compromised.
const CF_ID_RE = /^[0-9a-f]{32}$/;
const CF_UUID_RE = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/;

export function isCfId(value: unknown): value is string {
  return typeof value === "string" && CF_ID_RE.test(value);
}

export function isCfTunnelId(value: unknown): value is string {
  return typeof value === "string" && (CF_UUID_RE.test(value) || CF_ID_RE.test(value));
}

export function assertCfId(kind: string, value: string): string {
  if (!isCfId(value)) {
    throw new Error(`invalid_cf_${kind}_id`);
  }
  return value;
}

export function assertCfTunnelId(value: string): string {
  if (!isCfTunnelId(value)) {
    throw new Error("invalid_cf_tunnel_id");
  }
  return value;
}

interface CfResponse<T> {
  success: boolean;
  errors?: Array<{ code?: number; message?: string }>;
  result?: T;
}

function authHeaders(env: Env): HeadersInit {
  return {
    Authorization: `Bearer ${env.CF_API_TOKEN ?? ""}`,
    "content-type": "application/json"
  };
}

async function cfFetch<T>(env: Env, path: string, init: RequestInit = {}): Promise<T> {
  const response = await fetch(`${CF_API_BASE}${path}`, {
    ...init,
    headers: { ...authHeaders(env), ...(init.headers ?? {}) }
  });
  const payload = (await response.json().catch(() => null)) as CfResponse<T> | null;
  if (!response.ok || !payload || payload.success !== true) {
    const msg = payload?.errors?.[0]?.message || `cf_api_http_${response.status}`;
    throw new Error(msg);
  }
  return payload.result as T;
}

interface DnsRecord {
  id: string;
  name: string;
  type: string;
}

export async function deleteDnsRecordByName(
  env: Env,
  zoneId: string,
  name: string,
  type: string
): Promise<boolean> {
  assertCfId("zone", zoneId);
  const search = new URLSearchParams({ name, type });
  const records = await cfFetch<DnsRecord[]>(env, `/zones/${zoneId}/dns_records?${search.toString()}`);
  if (!records || records.length === 0) {
    return false;
  }
  let deleted = false;
  for (const rec of records) {
    // Defence in depth: don't trust the server-side filter for a destructive op.
    if (rec.name.toLowerCase() !== name.toLowerCase() || rec.type !== type) {
      continue;
    }
    await cfFetch(env, `/zones/${zoneId}/dns_records/${assertCfId("record", rec.id)}`, { method: "DELETE" });
    deleted = true;
  }
  return deleted;
}

export async function cleanupTunnelConnections(env: Env, tunnelId: string): Promise<void> {
  if (!env.CF_ACCOUNT_ID) {
    throw new Error("missing_cf_account_id");
  }
  assertCfTunnelId(tunnelId);
  await cfFetch(env, `/accounts/${env.CF_ACCOUNT_ID}/cfd_tunnel/${tunnelId}/connections`, {
    method: "DELETE"
  });
}

export async function deleteTunnel(env: Env, tunnelId: string): Promise<void> {
  if (!env.CF_ACCOUNT_ID) {
    throw new Error("missing_cf_account_id");
  }
  assertCfTunnelId(tunnelId);
  try {
    await cfFetch(env, `/accounts/${env.CF_ACCOUNT_ID}/cfd_tunnel/${tunnelId}`, {
      method: "DELETE"
    });
    return;
  } catch (e) {
    if (!/active connection/i.test(String(e))) {
      throw e;
    }
  }
  await cleanupTunnelConnections(env, tunnelId);
  await cfFetch(env, `/accounts/${env.CF_ACCOUNT_ID}/cfd_tunnel/${tunnelId}`, {
    method: "DELETE"
  });
}

export function hasCfCredentials(env: Env): boolean {
  return Boolean(env.CF_API_TOKEN && env.CF_ACCOUNT_ID);
}
