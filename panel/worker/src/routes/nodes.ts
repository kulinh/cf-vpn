import type {
  AgentHealthcheckResponse,
  AgentRotateResponse,
  AgentStatusResponse,
  AgentSyncResponse,
  Env,
  NodeRow
} from "../types";
import { all, one, nowTs } from "../lib/db";
import { callAgent } from "../lib/agent-client";
import { deleteDnsRecordByName, deleteTunnel, hasCfCredentials } from "../lib/cf-api";
import { error, isRecord, json, readJSON } from "../lib/http";
import { logEvent } from "../lib/events";
import { generateAdminHost, validateAdminHost } from "../lib/hosts";
import { generateHost, generateHy2Host, pickZone } from "../lib/host-gen";

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
  adminHost?: string;
  admin_host?: string;
  host?: string;
  vpn_host?: string;
  hy2_host?: string;
  zone?: string;
}

interface ZoneRow {
  name: string;
  cf_zone_id: string;
}

export async function listNodes(env: Env): Promise<Response> {
  const rows = await all<NodeRow>(
    env.DB.prepare("SELECT id,label,admin_host,vpn_host,hy2_host,hy2_port,hy2_obfs_pw,public_ip,zone,mode,status,last_seen_at,latency_ms,created_at FROM nodes ORDER BY id")
  );
  return json(rows);
}

export async function createNode(env: Env, request: Request): Promise<Response> {
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
  const adminHost = generateAdminHost(body.id);
  if (!adminHost) {
    return error(400, { error: "invalid_node_id", detail: "id must be a DNS label" });
  }
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

  await env.DB.prepare(
    "INSERT INTO nodes (id,label,admin_host,vpn_host,hy2_host,zone,mode,status,last_seen_at,latency_ms,created_at) VALUES (?, ?, ?, ?, ?, ?, 'direct', 'active', NULL, NULL, ?)"
  )
    .bind(body.id, body.label, adminHost, vpnHost, hy2Host, zoneName, nowTs())
    .run();
  return json({ ok: true, id: body.id, label: body.label, admin_host: adminHost, vpn_host: vpnHost, hy2_host: hy2Host, zone: zoneName }, 201);
}

export async function getNode(env: Env, id: string): Promise<Response> {
  const row = await one<NodeRow>(
    env.DB.prepare("SELECT id,label,admin_host,vpn_host,hy2_host,hy2_port,hy2_obfs_pw,public_ip,zone,mode,status,last_seen_at,latency_ms,created_at FROM nodes WHERE id = ?").bind(id)
  );
  if (!row) {
    return error(404, { error: "node_not_found", detail: id });
  }
  return json(row);
}

export async function patchNode(env: Env, id: string, request: Request): Promise<Response> {
  const existing = await one<NodeRow>(
    env.DB.prepare("SELECT id,label,admin_host,vpn_host,hy2_host,hy2_port,hy2_obfs_pw,public_ip,zone,mode,status,last_seen_at,latency_ms,created_at FROM nodes WHERE id = ?").bind(id)
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
  const updated = {
    label: body.label ?? existing.label,
    admin_host: body.admin_host ?? existing.admin_host,
    vpn_host: body.vpn_host ?? existing.vpn_host,
    zone: body.zone ?? existing.zone,
    status: body.status ?? existing.status
  };
  await env.DB.prepare("UPDATE nodes SET label=?, admin_host=?, vpn_host=?, zone=?, status=? WHERE id=?")
    .bind(updated.label, updated.admin_host, updated.vpn_host, updated.zone, updated.status, id)
    .run();
  return json({ ok: true });
}

async function resolveZoneIdForHost(env: Env, host: string): Promise<string | null> {
  const labels = host.split(".");
  for (let i = 0; i < labels.length - 1; i += 1) {
    const candidate = labels.slice(i).join(".");
    const row = await one<ZoneRow>(
      env.DB.prepare("SELECT name, cf_zone_id FROM zones WHERE name = ?").bind(candidate)
    );
    if (row) {
      return row.cf_zone_id;
    }
  }
  return null;
}

export async function deleteNode(env: Env, id: string): Promise<Response> {
  const row = await one<NodeRow>(
    env.DB.prepare(
      "SELECT id,label,admin_host,vpn_host,hy2_host,hy2_port,hy2_obfs_pw,public_ip,zone,mode,status,last_seen_at,latency_ms,created_at FROM nodes WHERE id = ?"
    ).bind(id)
  );
  if (!row) {
    return error(404, { error: "node_not_found", detail: id });
  }

  const warnings: string[] = [];

  if (!hasCfCredentials(env)) {
    warnings.push("CF cleanup skipped: CF_API_TOKEN/CF_ACCOUNT_ID not configured");
  } else {
    let tunnelUuid: string | null = null;
    let agentReachable = false;
    try {
      const status = await agentCall<AgentStatusResponse>(
        env,
        row.admin_host,
        "/admin/v1/status",
        { method: "GET" },
        5000
      );
      agentReachable = true;
      if (typeof status.tunnel_uuid === "string" && status.tunnel_uuid.length > 0) {
        tunnelUuid = status.tunnel_uuid;
      } else {
        warnings.push("Agent did not report tunnel_uuid; tunnel not deleted");
      }
    } catch (e) {
      warnings.push(`Agent unreachable, tunnel_uuid unknown: ${String(e)}`);
    }

    if (agentReachable) {
      try {
        await agentCall<{ ok: boolean }>(
          env,
          row.admin_host,
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

  const result = await env.DB.prepare("DELETE FROM nodes WHERE id = ?").bind(id).run();
  if (!result.success) {
    return error(500, { error: "delete_failed", detail: id });
  }
  return json({ ok: true, warnings });
}

async function getNodeOr404(env: Env, id: string): Promise<NodeRow | Response> {
  const row = await one<NodeRow>(
    env.DB.prepare("SELECT id,label,admin_host,vpn_host,hy2_host,hy2_port,hy2_obfs_pw,public_ip,zone,mode,status,last_seen_at,latency_ms,created_at FROM nodes WHERE id = ?").bind(id)
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
  return {
    hy2_host: typeof agent.hy2_host === "string" && agent.hy2_host ? agent.hy2_host : row.hy2_host,
    hy2_port: typeof agent.hy2_port === "number" ? agent.hy2_port : row.hy2_port,
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
  }
): Promise<void> {
  await env.DB.prepare(
    "UPDATE nodes SET status='active', vpn_host=?, zone=?, public_ip=?, mode=?, hy2_host=?, hy2_port=?, hy2_obfs_pw=?, last_seen_at=?, latency_ms=?, reality_pubkey=?, reality_sid=?, reality_sni=?, reality_dest=?, xhttp_path=? WHERE id=?"
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
    const status = await agentCall<AgentStatusResponse>(env, row.admin_host, "/admin/v1/status", { method: "GET" }, 5000);
    const mode = typeof status.mode === "string" && status.mode.length > 0 ? status.mode : row.mode ?? null;
    const syncRuntimeFields = mode === "direct";
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
      xhttp_path: syncRuntimeFields ? status.xhttp_path ?? row.xhttp_path : row.xhttp_path,
    });
    return json(status);
  } catch (e) {
    await env.DB.prepare("UPDATE nodes SET status='unreachable' WHERE id=?").bind(id).run();
    await logEvent(env, actor, "node.status", "error", { node_id: id, message: String(e) }, id);
    return error(502, { error: "agent_unreachable", detail: String(e) });
  }
}

export async function nodeHealthcheck(env: Env, id: string, actor: string): Promise<Response> {
  const row = await getNodeOr404(env, id);
  if (row instanceof Response) {
    return row;
  }
  try {
    const startedAt = Date.now();
    const out = await callAgent<AgentHealthcheckResponse>(env, row.admin_host, "/admin/v1/healthcheck", { method: "POST", body: "{}" }, 5000);
    const latencyMs = Math.max(1, Date.now() - startedAt);
    const now = nowTs();
    const measured = { ...out, latency_ms: latencyMs };
    await env.DB.prepare("UPDATE nodes SET last_seen_at=?, latency_ms=? WHERE id=?")
      .bind(now, latencyMs, id)
      .run();
    await logEvent(env, actor, "node.healthcheck", "ok", measured, id);
    return json(measured);
  } catch (e) {
    await logEvent(env, actor, "node.healthcheck", "error", { message: String(e) }, id);
    return error(502, { error: "healthcheck_failed", detail: String(e) });
  }
}

export async function nodeRotate(env: Env, id: string, request: Request, actor: string): Promise<Response> {
  const row = await getNodeOr404(env, id);
  if (row instanceof Response) {
    return row;
  }

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
  const oldZone = await one<ZoneRow>(env.DB.prepare("SELECT name, cf_zone_id FROM zones WHERE name = ?").bind(row.zone));
  try {
    const out = await agentCall<AgentRotateResponse>(
      env,
      row.admin_host,
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
      120000
    );
    await env.DB.prepare("UPDATE nodes SET vpn_host=?, hy2_host=?, hy2_port=?, hy2_obfs_pw=?, public_ip=?, zone=?, status='active', last_seen_at=? WHERE id=?")
      .bind(out.vpn_host, out.hy2_host, out.hy2_port, out.hy2_obfs_pw, out.public_ip, newZoneName, nowTs(), id)
      .run();
    await logEvent(env, actor, "node.rotate", "ok", { old_host: row.vpn_host, new_host: out.vpn_host, public_ip: out.public_ip }, id);
    return json({ vpn_host: out.vpn_host, hy2_host: out.hy2_host, public_ip: out.public_ip });
  } catch (e) {
    await logEvent(env, actor, "node.rotate", "error", { message: String(e) }, id);
    return error(502, { error: "rotate_failed", detail: String(e) });
  }
}

export async function nodeSync(env: Env, id: string, request: Request, actor: string): Promise<Response> {
  const row = await getNodeOr404(env, id);
  if (row instanceof Response) {
    return row;
  }
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

  try {
    const out = await agentCall<AgentSyncResponse>(
      env,
      row.admin_host,
      "/admin/v1/sync",
      { method: "POST", body: JSON.stringify({ users: body.users }) },
      120000
    );
    const syncRuntimeFields = row.mode === "direct";
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
      xhttp_path: syncRuntimeFields ? out.xhttp_path ?? row.xhttp_path : row.xhttp_path,
    });
    await logEvent(env, actor, "node.sync", "ok", out, id);
    return json(out);
  } catch (e) {
    await logEvent(env, actor, "node.sync", "error", { message: String(e) }, id);
    return error(502, { error: "sync_failed", detail: String(e) });
  }
}
