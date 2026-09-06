import type {
  AgentHealthcheckResponse,
  AgentRotateResponse,
  AgentStatusResponse,
  AgentSyncResponse,
  Env,
  NodeRow
} from "../types";
import { all, one, nowTs } from "../lib/db";
import { callAgent, isConfigError, MAX_TIMEOUT_MS } from "../lib/agent-client";
import { deleteDnsRecordByName, deleteTunnel, hasCfCredentials, isCfTunnelId } from "../lib/cf-api";
import { error, isRecord, json, readJSON } from "../lib/http";
import { eventStatement, logEvent } from "../lib/events";
import { generateAdminHost, validateAdminHost } from "../lib/hosts";
import { generateHost, generateHy2Host, pickZone } from "../lib/host-gen";

// A healthy sweep over the Cloudflare tunnel already measures 450–1700 ms, and
// a tunnel hiccup costs far more than that; 5s was low enough that the cron
// sweep flapped nodes between active/unreachable every few hours.
const HEALTHCHECK_TIMEOUT_MS = 15000;

const NODE_STATUSES = ["active", "disabled", "unreachable"] as const;
type NodeStatus = (typeof NODE_STATUSES)[number];

type AgentCaller = typeof callAgent;
type TestAgentCaller = (...args: Parameters<AgentCaller>) => Promise<unknown>;
let callAgentForTests: TestAgentCaller | null = null;

export function setCallAgentForTests(fn: TestAgentCaller | null): void {
  callAgentForTests = fn;
}

async function agentCall<T>(...args: Parameters<AgentCaller>): Promise<T> {
  if (callAgentForTests) {
    return await callAgentForTests(...args) as T;
  }
  return callAgent<T>(...args);
}
interface NodeInput {
  id: string;
  label: string;
  admin_host?: string;
  host?: string;
  vpn_host?: string;
  hy2_host?: string;
  zone?: string;
  agent_secret?: string;
}

interface ZoneRow {
  name: string;
  cf_zone_id: string;
}

// Strip data-plane / control-plane secrets before a node row leaves the Worker
// on a read endpoint. agent_secret (per-node bearer) and hy2_obfs_pw (Hysteria2
// obfuscation password) must never appear in listNodes/getNode responses — the
// internal callers (getNodeOr404, deleteNode, nodeStatus, …) still SELECT them.
function toPublicNode(row: NodeRow): Omit<NodeRow, "agent_secret" | "hy2_obfs_pw"> {
  const { agent_secret: _agentSecret, hy2_obfs_pw: _hy2ObfsPw, ...rest } = row;
  return rest;
}

export async function listNodes(env: Env): Promise<Response> {
  const rows = await all<NodeRow>(
    env.DB.prepare("SELECT id,label,admin_host,vpn_host,hy2_host,hy2_port,hy2_obfs_pw,public_ip,zone,mode,status,last_seen_at,latency_ms,created_at,agent_secret FROM nodes ORDER BY id")
  );
  return json(rows.map(toPublicNode));
}

export async function createNode(env: Env, request: Request, actor = "system"): Promise<Response> {
  let body: NodeInput;
  try {
    body = await readJSON<NodeInput>(request);
  } catch {
    return error(400, { error: "invalid_json", detail: "request body must be valid JSON" });
  }
  if (!isRecord(body)) {
    return error(400, { error: "invalid_node", detail: "request body must be a JSON object" });
  }
  if (!body.id || !body.label) {
    return error(400, { error: "invalid_node", detail: "id,label are required" });
  }
  let adminHost: string;
  if (typeof body.admin_host === "string" && body.admin_host.trim().length > 0) {
    adminHost = body.admin_host.trim();
    const hostError = validateAdminHost(adminHost, env);
    if (hostError) {
      return error(400, { error: "invalid_admin_host", detail: hostError });
    }
  } else {
    const generated = generateAdminHost(body.id);
    if (!generated) {
      return error(400, { error: "invalid_node_id", detail: "id must be a DNS label" });
    }
    adminHost = generated;
  }
  const agentSecretRaw = typeof body.agent_secret === "string" ? body.agent_secret.trim() : "";
  if (agentSecretRaw && agentSecretRaw.length < 16) {
    return error(400, { error: "invalid_agent_secret", detail: "agent_secret must be at least 16 chars" });
  }
  const agentSecret = agentSecretRaw || null;
  const hostOverride = body.host ?? body.vpn_host;
  const hasHost = typeof hostOverride === "string" && hostOverride.length > 0;
  const hasZone = typeof body.zone === "string" && body.zone.length > 0;
  if (hasHost !== hasZone) {
    return error(400, { error: "invalid_request", detail: "host and zone must be provided together" });
  }
  const exists = await one<{ id: string }>(env.DB.prepare("SELECT id FROM nodes WHERE id = ?").bind(body.id));
  if (exists) {
    return error(409, { error: "node_exists", detail: body.id });
  }
  const adminHostExists = await one<{ id: string }>(env.DB.prepare("SELECT id FROM nodes WHERE admin_host = ?").bind(adminHost));
  if (adminHostExists) {
    return error(409, { error: "admin_host_exists", detail: adminHost });
  }

  let vpnHost: string;
  let zoneName: string;
  const rng: (n: number) => Uint8Array = (n) => crypto.getRandomValues(new Uint8Array(n));
  if (hasHost && hasZone) {
    const overrideZone = await one<ZoneRow>(
      env.DB.prepare("SELECT name, cf_zone_id FROM zones WHERE name = ?").bind(body.zone)
    );
    if (!overrideZone) {
      return error(400, { error: "zone_not_found", detail: body.zone! });
    }
    vpnHost = hostOverride!;
    zoneName = overrideZone.name;
  } else {
    const candidates = await all<ZoneRow>(env.DB.prepare("SELECT name, cf_zone_id FROM zones WHERE enabled = 1"));
    if (candidates.length === 0) {
      return error(400, { error: "no_enabled_zones" });
    }
    const picked = pickZone(rng, candidates, "");
    vpnHost = generateHost(rng, picked.name);
    zoneName = picked.name;
  }
  const hy2HostOverride = typeof body.hy2_host === "string" ? body.hy2_host.trim() : "";
  const hy2Host = hy2HostOverride.length > 0 ? hy2HostOverride : generateHy2Host(rng, zoneName);

  try {
    await env.DB.prepare(
      "INSERT INTO nodes (id,label,admin_host,vpn_host,hy2_host,zone,mode,status,last_seen_at,latency_ms,created_at,agent_secret) VALUES (?, ?, ?, ?, ?, ?, 'direct', 'active', NULL, NULL, ?, ?)"
    )
      .bind(body.id, body.label, adminHost, vpnHost, hy2Host, zoneName, nowTs(), agentSecret)
      .run();
  } catch (e) {
    // The id / admin_host / vpn_host unique indexes can still fire on a race
    // between the pre-checks above and this INSERT — return 409, not a raw 500.
    if (/UNIQUE/i.test(String(e))) {
      return error(409, { error: "node_conflict", detail: "id, admin_host, or vpn_host already in use" });
    }
    throw e;
  }
  await logEvent(
    env,
    actor,
    "node.create",
    "ok",
    { node_id: body.id, label: body.label, admin_host: adminHost, vpn_host: vpnHost, hy2_host: hy2Host, zone: zoneName },
    body.id
  );
  return json({ ok: true, id: body.id, label: body.label, admin_host: adminHost, vpn_host: vpnHost, hy2_host: hy2Host, zone: zoneName }, 201);
}

export async function getNode(env: Env, id: string): Promise<Response> {
  const row = await one<NodeRow>(
    env.DB.prepare("SELECT id,label,admin_host,vpn_host,hy2_host,hy2_port,hy2_obfs_pw,public_ip,zone,mode,status,last_seen_at,latency_ms,created_at,agent_secret FROM nodes WHERE id = ?").bind(id)
  );
  if (!row) {
    return error(404, { error: "node_not_found", detail: id });
  }
  return json(toPublicNode(row));
}

export async function patchNode(env: Env, id: string, request: Request, actor = "system"): Promise<Response> {
  const existing = await one<NodeRow>(
    env.DB.prepare("SELECT id,label,admin_host,vpn_host,hy2_host,hy2_port,hy2_obfs_pw,public_ip,zone,mode,status,last_seen_at,latency_ms,created_at,agent_secret FROM nodes WHERE id = ?").bind(id)
  );
  if (!existing) {
    return error(404, { error: "node_not_found", detail: id });
  }
  let body: Partial<NodeInput & { status: string }>;
  try {
    body = await readJSON<Partial<NodeInput & { status: string }>>(request);
  } catch {
    return error(400, { error: "invalid_json", detail: "request body must be valid JSON" });
  }
  if (!isRecord(body)) {
    return error(400, { error: "invalid_node", detail: "request body must be a JSON object" });
  }
  if (body.admin_host !== undefined) {
    const hostError = validateAdminHost(body.admin_host, env);
    if (hostError) {
      return error(400, { error: "invalid_admin_host", detail: hostError });
    }
  }
  if (body.vpn_host !== undefined && (typeof body.vpn_host !== "string" || body.vpn_host.trim() === "")) {
    return error(400, { error: "invalid_node", detail: "vpn_host must be a non-empty string" });
  }
  // Validate zone against the zones table so a PATCH cannot point a node at a
  // non-existent zone (which would break rotate / DNS cleanup later).
  if (body.zone !== undefined) {
    if (typeof body.zone !== "string" || body.zone.trim() === "") {
      return error(400, { error: "invalid_node", detail: "zone must be a non-empty string" });
    }
    const zoneRow = await one<{ name: string }>(
      env.DB.prepare("SELECT name FROM zones WHERE name = ?").bind(body.zone)
    );
    if (!zoneRow) {
      return error(400, { error: "zone_not_found", detail: body.zone });
    }
  }
  // status has no CHECK constraint in D1; a typo here silently drops the node
  // out of `WHERE status='active'`, so new users are never provisioned to it.
  if (body.status !== undefined && !NODE_STATUSES.includes(body.status as NodeStatus)) {
    return error(400, { error: "invalid_status", detail: `status must be one of ${NODE_STATUSES.join(", ")}` });
  }
  const updated = {
    label: body.label ?? existing.label,
    admin_host: body.admin_host ?? existing.admin_host,
    vpn_host: body.vpn_host ?? existing.vpn_host,
    zone: body.zone ?? existing.zone,
    status: body.status ?? existing.status
  };
  try {
    await env.DB.prepare("UPDATE nodes SET label=?, admin_host=?, vpn_host=?, zone=?, status=? WHERE id=?")
      .bind(updated.label, updated.admin_host, updated.vpn_host, updated.zone, updated.status, id)
      .run();
  } catch (e) {
    // Both admin_host and vpn_host carry UNIQUE indexes — identify the offending
    // column from the D1 message ("UNIQUE constraint failed: nodes.<col>")
    // instead of always blaming vpn_host.
    const msg = String(e);
    if (/UNIQUE/i.test(msg)) {
      const col = msg.match(/UNIQUE constraint failed:\s*nodes\.(\w+)/i)?.[1];
      if (col === "admin_host") {
        return error(409, { error: "admin_host_exists", detail: updated.admin_host });
      }
      if (col === "vpn_host") {
        return error(409, { error: "vpn_host_exists", detail: updated.vpn_host });
      }
      return error(409, { error: "node_conflict", detail: "admin_host or vpn_host already in use" });
    }
    throw e;
  }
  await logEvent(env, actor, "node.update", "ok", { node_id: id, changes: updated }, id);
  return json({ ok: true });
}

async function resolveZoneIdForHost(env: Env, host: string): Promise<string | null> {
  const labels = host.split(".");
  // Candidate zone names, longest suffix first so the most specific match wins.
  const candidates: string[] = [];
  for (let i = 0; i < labels.length - 1; i += 1) {
    candidates.push(labels.slice(i).join("."));
  }
  if (candidates.length === 0) {
    return null;
  }
  // One round-trip instead of one query per label (deleteNode calls this 3×).
  const placeholders = candidates.map(() => "?").join(",");
  const rows = await all<ZoneRow>(
    env.DB.prepare(`SELECT name, cf_zone_id FROM zones WHERE name IN (${placeholders})`).bind(...candidates)
  );
  const byName = new Map(rows.map((r) => [r.name, r.cf_zone_id]));
  for (const candidate of candidates) {
    const id = byName.get(candidate);
    if (id) {
      return id;
    }
  }
  return null;
}

export async function deleteNode(env: Env, id: string, actor = "system"): Promise<Response> {
  const row = await one<NodeRow>(
    env.DB.prepare(
      "SELECT id,label,admin_host,vpn_host,hy2_host,hy2_port,hy2_obfs_pw,public_ip,zone,mode,status,last_seen_at,latency_ms,created_at,agent_secret,tunnel_uuid FROM nodes WHERE id = ?"
    ).bind(id)
  );
  if (!row) {
    return error(404, { error: "node_not_found", detail: id });
  }

  const warnings: string[] = [];

  if (!hasCfCredentials(env)) {
    warnings.push("CF cleanup skipped: CF_API_TOKEN/CF_ACCOUNT_ID not configured");
  } else {
    // Fall back to the persisted tunnel_uuid (captured on the last status sync)
    // so we can still delete the tunnel when the agent is unreachable now.
    let tunnelUuid: string | null = row.tunnel_uuid && row.tunnel_uuid.length > 0 ? row.tunnel_uuid : null;
    let agentReachable = false;
    try {
      const status = await agentCall<AgentStatusResponse>(
        env,
        { adminHost: row.admin_host, agentSecret: row.agent_secret, nodeId: row.id },
        "/admin/v1/status",
        { method: "GET" },
        5000
      );
      agentReachable = true;
      if (typeof status.tunnel_uuid === "string" && status.tunnel_uuid.length > 0) {
        // Same rule as persistNodeRuntime: only a well-formed tunnel id may be
        // interpolated into the Cloudflare API path (see lib/cf-api.ts).
        if (isCfTunnelId(status.tunnel_uuid)) {
          tunnelUuid = status.tunnel_uuid;
        } else {
          warnings.push("Agent reported a malformed tunnel_uuid; ignored");
        }
      } else if (!tunnelUuid) {
        warnings.push("Agent did not report tunnel_uuid; tunnel not deleted");
      }
    } catch (e) {
      if (tunnelUuid) {
        warnings.push(`Agent unreachable; using persisted tunnel_uuid for cleanup: ${String(e)}`);
      } else {
        warnings.push(`Agent unreachable, tunnel_uuid unknown: ${String(e)}`);
      }
    }

    if (agentReachable) {
      try {
        await agentCall<{ ok: boolean }>(
          env,
          { adminHost: row.admin_host, agentSecret: row.agent_secret, nodeId: row.id },
          "/admin/v1/shutdown-tunnel",
          { method: "POST", body: "{}" },
          15000
        );
      } catch (e) {
        warnings.push(`Stop cloudflared on agent failed: ${String(e)}`);
      }
    }

    const vpnZoneId = await resolveZoneIdForHost(env, row.vpn_host);
    if (vpnZoneId) {
      try {
        await deleteDnsRecordByName(env, vpnZoneId, row.vpn_host, "A");
      } catch (e) {
        warnings.push(`Delete A ${row.vpn_host} failed: ${String(e)}`);
      }
    } else {
      warnings.push(`Zone for ${row.vpn_host} not found in zones table`);
    }

    if (row.hy2_host && row.hy2_host !== row.vpn_host) {
      const hy2ZoneId = await resolveZoneIdForHost(env, row.hy2_host);
      if (hy2ZoneId) {
        try {
          await deleteDnsRecordByName(env, hy2ZoneId, row.hy2_host, "A");
        } catch (e) {
          warnings.push(`Delete A ${row.hy2_host} failed: ${String(e)}`);
        }
      } else {
        warnings.push(`Zone for ${row.hy2_host} not found in zones table`);
      }
    }

    const adminZoneId = await resolveZoneIdForHost(env, row.admin_host);
    if (adminZoneId) {
      try {
        await deleteDnsRecordByName(env, adminZoneId, row.admin_host, "CNAME");
      } catch (e) {
        warnings.push(`Delete CNAME ${row.admin_host} failed: ${String(e)}`);
      }
    } else {
      warnings.push(`Zone for ${row.admin_host} not found in zones table`);
    }

    if (tunnelUuid) {
      try {
        await deleteTunnel(env, tunnelUuid);
      } catch (e) {
        warnings.push(`Delete tunnel ${tunnelUuid} failed: ${String(e)}`);
      }
    }
  }

  // One transaction: as two separate statements, a failure of the second left
  // the membership rows gone but the node row (and its agent credentials) in
  // place — a state no retry converges out of.
  let results: D1Result[];
  try {
    results = await env.DB.batch([
      env.DB.prepare("DELETE FROM user_nodes WHERE node_id = ?").bind(id),
      env.DB.prepare("DELETE FROM nodes WHERE id = ?").bind(id)
    ]);
  } catch (e) {
    return error(500, { error: "delete_failed", detail: String(e) });
  }
  if (results.some((r) => !r.success)) {
    return error(500, { error: "delete_failed", detail: id });
  }
  await logEvent(env, actor, "node.delete", warnings.length > 0 ? "partial" : "ok", { node_id: id, warnings }, id);
  return json({ ok: true, warnings });
}

async function getNodeOr404(env: Env, id: string): Promise<NodeRow | Response> {
  const row = await one<NodeRow>(
    env.DB.prepare("SELECT id,label,admin_host,vpn_host,hy2_host,hy2_port,hy2_obfs_pw,public_ip,zone,mode,status,last_seen_at,latency_ms,created_at,agent_secret,tunnel_uuid FROM nodes WHERE id = ?").bind(id)
  );
  if (!row) {
    return error(404, { error: "node_not_found", detail: id });
  }
  return row;
}

interface Hy2Runtime {
  hy2_host: string | null;
  hy2_port: number | null;
  hy2_obfs_pw: string | null;
}

function mergeHy2Runtime(
  row: NodeRow,
  agent: { hy2_host?: string; hy2_port?: number; hy2_obfs_pw?: string }
): Hy2Runtime {
  // Treat hy2_port=0 as "unknown" — the agent emits 0 when env["HY2_PORT"] is
  // empty (parseInt swallows the error and returns the zero value). Persisting
  // 0 would break subscription URI builders that emit ":0".
  return {
    hy2_host: typeof agent.hy2_host === "string" && agent.hy2_host ? agent.hy2_host : row.hy2_host,
    hy2_port: typeof agent.hy2_port === "number" && agent.hy2_port > 0 ? agent.hy2_port : row.hy2_port,
    hy2_obfs_pw: typeof agent.hy2_obfs_pw === "string" && agent.hy2_obfs_pw ? agent.hy2_obfs_pw : row.hy2_obfs_pw,
  };
}

async function persistNodeRuntime(
  env: Env,
  id: string,
  fields: {
    vpn_host: string;
    zone: string;
    public_ip: string | null;
    mode: string | null;
    hy2: Hy2Runtime;
    last_seen_at: number;
    latency_ms: number | null;
    reality_pubkey: string | null;
    reality_sid: string | null;
    reality_sni: string | null;
    reality_dest: string | null;
    xhttp_path: string | null;
    tunnel_uuid: string | null;
  }
): Promise<void> {
  await env.DB.prepare(
    "UPDATE nodes SET status='active', vpn_host=?, zone=?, public_ip=?, mode=?, hy2_host=?, hy2_port=?, hy2_obfs_pw=?, last_seen_at=?, latency_ms=?, reality_pubkey=?, reality_sid=?, reality_sni=?, reality_dest=?, xhttp_path=?, tunnel_uuid=? WHERE id=?"
  )
    .bind(
      fields.vpn_host,
      fields.zone,
      fields.public_ip,
      fields.mode,
      fields.hy2.hy2_host,
      fields.hy2.hy2_port,
      fields.hy2.hy2_obfs_pw,
      fields.last_seen_at,
      fields.latency_ms,
      fields.reality_pubkey,
      fields.reality_sid,
      fields.reality_sni,
      fields.reality_dest,
      fields.xhttp_path,
      fields.tunnel_uuid,
      id
    )
    .run();
}

export async function nodeStatus(env: Env, id: string, actor: string): Promise<Response> {
  const row = await getNodeOr404(env, id);
  if (row instanceof Response) {
    return row;
  }
  try {
    const status = await agentCall<AgentStatusResponse>(env, { adminHost: row.admin_host, agentSecret: row.agent_secret, nodeId: row.id }, "/admin/v1/status", { method: "GET" }, 5000);
    const mode = typeof status.mode === "string" && status.mode.length > 0 ? status.mode : row.mode ?? null;
    const syncRuntimeFields = mode === "direct";
    const syncCloudflareFields = mode === "cloudflare";
    const hasStatusHost = typeof status.vpn_host === "string" && status.vpn_host.length > 0;
    const hasStatusZone = typeof status.zone === "string" && status.zone.length > 0;
    await persistNodeRuntime(env, id, {
      vpn_host: syncRuntimeFields && hasStatusHost && hasStatusZone ? status.vpn_host : row.vpn_host,
      zone: syncRuntimeFields && hasStatusHost && hasStatusZone ? status.zone! : row.zone,
      public_ip: syncRuntimeFields && status.public_ip ? status.public_ip : row.public_ip,
      mode,
      hy2: mergeHy2Runtime(row, status),
      last_seen_at: nowTs(),
      latency_ms: row.latency_ms ?? null,
      reality_pubkey: syncRuntimeFields ? status.reality_pubkey ?? row.reality_pubkey : row.reality_pubkey,
      reality_sid: syncRuntimeFields ? status.reality_sid ?? row.reality_sid : row.reality_sid,
      reality_sni: syncRuntimeFields ? status.reality_sni ?? row.reality_sni : row.reality_sni,
      reality_dest: syncRuntimeFields ? status.reality_dest ?? row.reality_dest : row.reality_dest,
      xhttp_path: syncCloudflareFields ? status.xhttp_path ?? row.xhttp_path : row.xhttp_path,
      // The agent is only as trustworthy as the VPS it runs on: a compromised
      // node could report a tunnel_uuid crafted to escape the Cloudflare API
      // path template on the next deleteNode. Persist it only if it looks like
      // a tunnel id; otherwise keep whatever we already had.
      tunnel_uuid: isCfTunnelId(status.tunnel_uuid) ? status.tunnel_uuid : row.tunnel_uuid,
    });
    return json(status);
  } catch (e) {
    // Only flip the row to "unreachable" on actual transport errors. Auth /
    // validation 4xxs throw `agent_http_<code>` from agent-client and should
    // not erase a previously-good "active" status — they signal a config
    // problem, not a node outage.
    const msg = String(e);
    const looksTransportError = !isConfigError(e);
    if (looksTransportError) {
      await env.DB.prepare("UPDATE nodes SET status='unreachable' WHERE id=?").bind(id).run();
    }
    await logEvent(env, actor, "node.status", "error", { node_id: id, message: msg }, id);
    return error(502, { error: "agent_unreachable", detail: msg });
  }
}

export async function nodeHealthcheck(env: Env, id: string, actor: string): Promise<Response> {
  const row = await getNodeOr404(env, id);
  if (row instanceof Response) {
    return row;
  }
  try {
    const startedAt = Date.now();
    const out = await agentCall<AgentHealthcheckResponse>(env, { adminHost: row.admin_host, agentSecret: row.agent_secret, nodeId: row.id }, "/admin/v1/healthcheck", { method: "POST", body: "{}" }, HEALTHCHECK_TIMEOUT_MS);
    const latencyMs = Math.max(1, Date.now() - startedAt);
    const now = nowTs();
    const measured = { ...out, latency_ms: latencyMs };
    const wasUnreachable = row.status === "unreachable";
    await env.DB.prepare("UPDATE nodes SET status='active', last_seen_at=?, latency_ms=? WHERE id=?")
      .bind(now, latencyMs, id)
      .run();
    if (wasUnreachable) {
      await logEvent(env, actor, "node.healthcheck.recover", "ok", measured, id);
    }
    return json(measured);
  } catch (e) {
    await logEvent(env, actor, "node.healthcheck", "error", { message: String(e) }, id);
    return error(502, { error: "healthcheck_failed", detail: String(e) });
  }
}

// Cron-driven fleet sweep: refreshes last_seen_at/latency_ms for every node so
// the panel's freshness data stays live without anyone clicking healthcheck
// (before this, last_seen_at only moved on manual panel actions). Mirrors the
// manual routes' semantics: success → active, transport failure → unreachable
// (agent_http_4xx signals a config problem, not an outage, and leaves status
// alone). Events are logged only on status transitions so a dead node doesn't
// emit a log line every sweep.
interface SweepRow {
  id: string;
  admin_host: string;
  agent_secret: string | null;
  status: string;
  consecutive_failures: number | null;
}

// A node must miss two consecutive sweeps before it is called unreachable: a
// single tunnel hiccup (agent_http_520/502, one slow round-trip) produced 79
// error→recover pairs in 29 days of prod events, i.e. ~99% noise.
const UNREACHABLE_AFTER_FAILURES = 2;

export async function sweepNodesHealth(env: Env): Promise<void> {
  const { results } = await env.DB.prepare(
    "SELECT id, admin_host, agent_secret, status, consecutive_failures FROM nodes"
  ).all<SweepRow>();
  const rows = results ?? [];

  const settled = await Promise.allSettled(
    rows.map(async (row): Promise<D1PreparedStatement[]> => {
      const writes: D1PreparedStatement[] = [];
      const failures = (row.consecutive_failures ?? 0) + 1;
      try {
        const startedAt = Date.now();
        await agentCall<AgentHealthcheckResponse>(
          env,
          { adminHost: row.admin_host, agentSecret: row.agent_secret, nodeId: row.id },
          "/admin/v1/healthcheck",
          { method: "POST", body: "{}" },
          HEALTHCHECK_TIMEOUT_MS
        );
        const latencyMs = Math.max(1, Date.now() - startedAt);
        writes.push(
          env.DB.prepare(
            "UPDATE nodes SET status='active', last_seen_at=?, latency_ms=?, consecutive_failures=0 WHERE id=?"
          ).bind(nowTs(), latencyMs, row.id)
        );
        if (row.status === "unreachable") {
          writes.push(
            eventStatement(env, "cron", "node.healthcheck.recover", "ok", { latency_ms: latencyMs }, row.id)
          );
        }
        return writes;
      } catch (e) {
        const msg = String(e);
        if (isConfigError(e)) {
          // A config problem (bad agent_secret, validation) is not an outage:
          // never flip status. But it used to be invisible — status stayed
          // 'active' while last_seen_at froze — so log exactly once, on the
          // first consecutive occurrence.
          writes.push(
            env.DB.prepare("UPDATE nodes SET consecutive_failures=? WHERE id=?").bind(failures, row.id)
          );
          if (failures === 1) {
            writes.push(
              eventStatement(env, "cron", "node.healthcheck", "error", { message: msg, config_error: true }, row.id)
            );
          }
          return writes;
        }
        const flip = failures >= UNREACHABLE_AFTER_FAILURES && row.status !== "unreachable";
        writes.push(
          flip
            ? env.DB.prepare("UPDATE nodes SET status='unreachable', consecutive_failures=? WHERE id=?").bind(failures, row.id)
            : env.DB.prepare("UPDATE nodes SET consecutive_failures=? WHERE id=?").bind(failures, row.id)
        );
        if (flip) {
          writes.push(
            eventStatement(env, "cron", "node.healthcheck", "error", { message: msg, consecutive_failures: failures }, row.id)
          );
        }
        return writes;
      }
    })
  );

  const writes: D1PreparedStatement[] = [];
  settled.forEach((result, i) => {
    if (result.status === "fulfilled") {
      writes.push(...result.value);
      return;
    }
    // The allSettled array used to be discarded, so a thrown statement builder
    // vanished without a trace.
    console.error("sweep failed for node", rows[i]?.id, String(result.reason));
  });

  if (writes.length === 0) {
    return;
  }
  // One transaction, one subrequest — the old code issued N UPDATEs plus N
  // event INSERTs, which alone approached the free-plan subrequest cap.
  try {
    await env.DB.batch(writes);
  } catch (e) {
    console.error("sweep batch write failed", String(e));
  }
}

export async function nodeRotate(env: Env, id: string, request: Request, actor: string): Promise<Response> {
  // Optional override body — empty body is fine (auto path).
  let override: { host?: string; zone?: string } = {};
  const raw = await request.text();
  if (raw.trim() !== "") {
    try {
      const parsed = JSON.parse(raw);
      if (isRecord(parsed)) {
        override = parsed as { host?: string; zone?: string };
      }
    } catch {
      override = {};
    }
  }
  return nodeRotateCore(env, id, override, actor);
}

export async function nodeRotateCore(
  env: Env,
  id: string,
  override: { host?: string; zone?: string },
  actor: string
): Promise<Response> {
  const row = await getNodeOr404(env, id);
  if (row instanceof Response) {
    return row;
  }
  const hasHost = typeof override.host === "string" && override.host.length > 0;
  const hasZone = typeof override.zone === "string" && override.zone.length > 0;
  if (hasHost !== hasZone) {
    return error(400, { error: "invalid_request", detail: "host and zone must be provided together" });
  }

  let newHost: string;
  let newZoneID: string;
  let newZoneName: string;
  const rng: (n: number) => Uint8Array = (n) => crypto.getRandomValues(new Uint8Array(n));

  if (hasHost && hasZone) {
    const overrideZone = await one<ZoneRow>(
      env.DB.prepare("SELECT name, cf_zone_id FROM zones WHERE name = ?").bind(override.zone)
    );
    if (!overrideZone) {
      return error(400, { error: "zone_not_found", detail: override.zone! });
    }
    newHost = override.host!;
    newZoneID = overrideZone.cf_zone_id;
    newZoneName = overrideZone.name;
  } else {
    const candidates = await all<ZoneRow>(
      env.DB.prepare("SELECT name, cf_zone_id FROM zones WHERE enabled = 1 AND name != ?").bind(row.zone)
    );
    if (candidates.length === 0) {
      return error(400, {
        error: "rotate_requires_multi_zone",
        detail: "enable at least one other zone before rotating"
      });
    }
    const picked = pickZone(rng, candidates, "");
    newHost = generateHost(rng, picked.name);
    newZoneID = picked.cf_zone_id;
    newZoneName = picked.name;
  }

  const newHy2Host = generateHy2Host(rng, newZoneName);
  // host-gen draws 4 random bytes and never checks for a collision, but vpn_host
  // carries a UNIQUE index — catch that here rather than after the agent has
  // already moved the node.
  const hostTaken = await one<{ id: string }>(
    env.DB.prepare("SELECT id FROM nodes WHERE vpn_host = ? AND id != ?").bind(newHost, id)
  );
  if (hostTaken) {
    return error(409, { error: "vpn_host_exists", detail: newHost });
  }
  const oldZone = await one<ZoneRow>(env.DB.prepare("SELECT name, cf_zone_id FROM zones WHERE name = ?").bind(row.zone));

  let out: AgentRotateResponse;
  try {
    out = await agentCall<AgentRotateResponse>(
      env,
      { adminHost: row.admin_host, agentSecret: row.agent_secret, nodeId: row.id },
      "/admin/v1/rotate-domain",
      {
        method: "POST",
        body: JSON.stringify({
          new_host: newHost,
          new_zone_id: newZoneID,
          old_host: row.vpn_host,
          old_zone_id: oldZone?.cf_zone_id ?? "",
          new_hy2_host: newHy2Host,
          new_hy2_zone: newZoneName,
          new_hy2_zone_id: newZoneID,
          old_hy2_host: row.hy2_host ?? "",
          old_hy2_zone_id: oldZone?.cf_zone_id ?? ""
        })
      },
      MAX_TIMEOUT_MS
    );
  } catch (e) {
    await logEvent(env, actor, "node.rotate", "error", { message: String(e) }, id);
    return error(502, { error: "rotate_failed", detail: String(e) });
  }

  // The agent has already moved the node at this point. A failed write here is
  // NOT "rotate failed": reporting it as such invites the operator to retry,
  // which rotates a second time and invalidates every subscription that still
  // pointed at the first new host. Log the new host so the row can be
  // reconciled by hand, and return a distinct error.
  const hy2 = mergeHy2Runtime(row, out);
  try {
    await env.DB.prepare("UPDATE nodes SET vpn_host=?, hy2_host=?, hy2_port=?, hy2_obfs_pw=?, public_ip=?, zone=?, status='active', last_seen_at=? WHERE id=?")
      .bind(out.vpn_host, hy2.hy2_host, hy2.hy2_port, hy2.hy2_obfs_pw, out.public_ip, newZoneName, nowTs(), id)
      .run();
  } catch (e) {
    await logEvent(
      env,
      actor,
      "node.rotate",
      "partial",
      {
        old_host: row.vpn_host,
        new_host: out.vpn_host,
        new_hy2_host: hy2.hy2_host,
        public_ip: out.public_ip,
        zone: newZoneName,
        persist_error: String(e)
      },
      id
    );
    return error(500, {
      error: "rotate_persist_failed",
      detail: "node rotated but D1 was not updated — do NOT retry, reconcile the row",
      vpn_host: out.vpn_host,
      hy2_host: hy2.hy2_host,
      public_ip: out.public_ip,
      zone: newZoneName
    });
  }
  await logEvent(env, actor, "node.rotate", "ok", { old_host: row.vpn_host, new_host: out.vpn_host, public_ip: out.public_ip }, id);
  return json({ vpn_host: out.vpn_host, hy2_host: hy2.hy2_host, public_ip: out.public_ip });
}

export async function nodeSync(env: Env, id: string, request: Request, actor: string): Promise<Response> {
  let body: { users: Array<{ name: string; vless_uuid: string; hy2_pw: string }> };
  try {
    body = await readJSON<{ users: Array<{ name: string; vless_uuid: string; hy2_pw: string }> }>(request);
  } catch {
    return error(400, { error: "invalid_json", detail: "request body must be valid JSON" });
  }
  if (!isRecord(body)) {
    return error(400, { error: "invalid_sync_payload", detail: "request body must be a JSON object" });
  }
  if (!Array.isArray(body.users)) {
    return error(400, { error: "invalid_sync_payload", detail: "users must be an array" });
  }
  const invalid = body.users.some(
    (u) => !u || typeof u.name !== "string" || typeof u.vless_uuid !== "string" || typeof u.hy2_pw !== "string"
  );
  if (invalid) {
    return error(400, { error: "invalid_sync_payload", detail: "each user must include name, vless_uuid, hy2_pw" });
  }
  return nodeSyncCore(env, id, body.users, actor);
}

export async function nodeSyncCore(
  env: Env,
  id: string,
  users: Array<{ name: string; vless_uuid: string; hy2_pw: string }>,
  actor: string
): Promise<Response> {
  const row = await getNodeOr404(env, id);
  if (row instanceof Response) {
    return row;
  }
  try {
    const out = await agentCall<AgentSyncResponse>(
      env,
      { adminHost: row.admin_host, agentSecret: row.agent_secret, nodeId: row.id },
      "/admin/v1/sync",
      { method: "POST", body: JSON.stringify({ users }) },
      MAX_TIMEOUT_MS
    );
    const syncRuntimeFields = row.mode === "direct";
    const syncCloudflareFields = row.mode === "cloudflare";
    const hasSyncHost = typeof out.vpn_host === "string" && out.vpn_host.length > 0;
    await persistNodeRuntime(env, id, {
      vpn_host: syncRuntimeFields && hasSyncHost ? out.vpn_host : row.vpn_host,
      zone: syncRuntimeFields && hasSyncHost ? out.vpn_host.split(".").slice(-2).join(".") : row.zone,
      public_ip: syncRuntimeFields ? out.public_ip || row.public_ip : row.public_ip,
      mode: row.mode ?? null,
      hy2: mergeHy2Runtime(row, out),
      last_seen_at: nowTs(),
      latency_ms: row.latency_ms ?? null,
      reality_pubkey: syncRuntimeFields ? out.reality_pubkey ?? row.reality_pubkey : row.reality_pubkey,
      reality_sid: syncRuntimeFields ? out.reality_sid ?? row.reality_sid : row.reality_sid,
      reality_sni: syncRuntimeFields ? out.reality_sni ?? row.reality_sni : row.reality_sni,
      reality_dest: syncRuntimeFields ? out.reality_dest ?? row.reality_dest : row.reality_dest,
      xhttp_path: syncCloudflareFields ? out.xhttp_path ?? row.xhttp_path : row.xhttp_path,
      tunnel_uuid: row.tunnel_uuid, // sync response carries no tunnel_uuid; preserve persisted value
    });
    // Log a safe projection — never the full AgentSyncResponse, which carries
    // hy2_obfs_pw (a data-plane secret) into the events audit log readable by
    // every panel actor (matches the redacted node.rotate event).
    await logEvent(
      env,
      actor,
      "node.sync",
      "ok",
      { vpn_host: out.vpn_host, public_ip: out.public_ip, hy2_host: out.hy2_host, hy2_port: out.hy2_port, users: out.users, mode: out.mode },
      id
    );
    return json(out);
  } catch (e) {
    await logEvent(env, actor, "node.sync", "error", { message: String(e) }, id);
    return error(502, { error: "sync_failed", detail: String(e) });
  }
}
