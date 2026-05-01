import type { Env } from "../types";

const CF_API_BASE = "https://api.cloudflare.com/client/v4";

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
  const search = new URLSearchParams({ name, type });
  const records = await cfFetch<DnsRecord[]>(env, `/zones/${zoneId}/dns_records?${search.toString()}`);
  if (!records || records.length === 0) {
    return false;
  }
  for (const rec of records) {
    await cfFetch(env, `/zones/${zoneId}/dns_records/${rec.id}`, { method: "DELETE" });
  }
  return true;
}

export async function cleanupTunnelConnections(env: Env, tunnelId: string): Promise<void> {
  if (!env.CF_ACCOUNT_ID) {
    throw new Error("missing_cf_account_id");
  }
  await cfFetch(env, `/accounts/${env.CF_ACCOUNT_ID}/cfd_tunnel/${tunnelId}/connections`, {
    method: "DELETE"
  });
}

export async function deleteTunnel(env: Env, tunnelId: string): Promise<void> {
  if (!env.CF_ACCOUNT_ID) {
    throw new Error("missing_cf_account_id");
  }
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
