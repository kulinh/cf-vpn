import type {
  AgentHealthcheckResponse,
  AgentRotateResponse,
  AgentStatusResponse,
  AgentSyncResponse,
  Env,
  NodeRow
} from "../types";
import { all, one, nowTs, randomHex } from "../lib/db";
import { callAgent } from "../lib/agent-client";
import { error, isRecord, json, readJSON } from "../lib/http";
import { logEvent } from "../lib/events";
import { validateAdminHost } from "../lib/hosts";

interface NodeInput {
  id: string;
  label: string;
  admin_host: string;
  vpn_host: string;
  zone: string;
}

interface ZoneRow {
  name: string;
  cf_zone_id: string;
}

const NODE_SELECT_FIELDS = "id,label,admin_host,vpn_host,zone,status,last_seen_at,latency_ms,created_at,public_ip,mode,hy2_host,hy2_port,hy2_obfs_pw";

export async function listNodes(env: Env): Promise<Response> {
  const rows = await all<NodeRow>(
    env.DB.prepare(`SELECT ${NODE_SELECT_FIELDS} FROM nodes ORDER BY id`)
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
  if (!body.id || !body.label || !body.admin_host || !body.vpn_host || !body.zone) {
    return error(400, { error: "invalid_node", detail: "id,label,admin_host,vpn_host,zone are required" });
  }
  const hostError = validateAdminHost(body.admin_host, env);
  if (hostError) {
    return error(400, { error: "invalid_admin_host", detail: hostError });
  }
  const exists = await one<{ id: string }>(env.DB.prepare("SELECT id FROM nodes WHERE id = ?").bind(body.id));
  if (exists) {
    return error(409, { error: "node_exists", detail: body.id });
  }

  await env.DB.prepare(
    "INSERT INTO nodes (id,label,admin_host,vpn_host,zone,status,last_seen_at,latency_ms,created_at) VALUES (?, ?, ?, ?, ?, 'active', NULL, NULL, ?)"
  )
    .bind(body.id, body.label, body.admin_host, body.vpn_host, body.zone, nowTs())
    .run();
  return json({ ok: true }, 201);
}

export async function getNode(env: Env, id: string): Promise<Response> {
  const row = await one<NodeRow>(
    env.DB.prepare(`SELECT ${NODE_SELECT_FIELDS} FROM nodes WHERE id = ?`).bind(id)
  );
  if (!row) {
    return error(404, { error: "node_not_found", detail: id });
  }
  return json(row);
}

export async function patchNode(env: Env, id: string, request: Request): Promise<Response> {
  const existing = await one<NodeRow>(
    env.DB.prepare(`SELECT ${NODE_SELECT_FIELDS} FROM nodes WHERE id = ?`).bind(id)
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

export async function deleteNode(env: Env, id: string): Promise<Response> {
  const result = await env.DB.prepare("DELETE FROM nodes WHERE id = ?").bind(id).run();
  if (!result.success) {
    return error(500, { error: "delete_failed", detail: id });
  }
  return json({ ok: true });
}

async function getNodeOr404(env: Env, id: string): Promise<NodeRow | Response> {
  const row = await one<NodeRow>(
    env.DB.prepare(`SELECT ${NODE_SELECT_FIELDS} FROM nodes WHERE id = ?`).bind(id)
  );
  if (!row) {
    return error(404, { error: "node_not_found", detail: id });
  }
  return row;
}

export async function nodeStatus(env: Env, id: string, actor: string): Promise<Response> {
  const row = await getNodeOr404(env, id);
  if (row instanceof Response) {
    return row;
  }
  try {
    const t0 = Date.now();
    const status = await callAgent<AgentStatusResponse>(env, row.admin_host, "/admin/v1/status", { method: "GET" }, 5000);
    const latency_ms = Date.now() - t0;
    const now = nowTs();
    await env.DB.prepare("UPDATE nodes SET status='active', last_seen_at=?, latency_ms=? WHERE id=?")
      .bind(now, latency_ms, id)
      .run();
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
    const t0 = Date.now();
    const out = await callAgent<AgentHealthcheckResponse>(env, row.admin_host, "/admin/v1/healthcheck", { method: "POST", body: "{}" }, 5000);
    const latency_ms = Date.now() - t0;
    const now = nowTs();
    await env.DB.prepare("UPDATE nodes SET last_seen_at=?, latency_ms=? WHERE id=?")
      .bind(now, latency_ms, id)
      .run();
    await logEvent(env, actor, "node.healthcheck", "ok", { ...out, latency_ms }, id);
    return json({ ...out, latency_ms });
  } catch (e) {
    await logEvent(env, actor, "node.healthcheck", "error", { message: String(e) }, id);
    return error(502, { error: "healthcheck_failed", detail: String(e) });
  }
}

export async function nodeRotate(env: Env, id: string, actor: string): Promise<Response> {
  const row = await getNodeOr404(env, id);
  if (row instanceof Response) {
    return row;
  }
  const zone = await one<ZoneRow>(
    env.DB.prepare("SELECT name, cf_zone_id FROM zones WHERE enabled = 1 ORDER BY RANDOM() LIMIT 1")
  );
  if (!zone) {
    return error(400, { error: "no_enabled_zones", detail: "add enabled zone in settings" });
  }

  const newHost = `${randomHex(4)}.${zone.name}`;
  try {
    const out = await callAgent<AgentRotateResponse>(
      env,
      row.admin_host,
      "/admin/v1/rotate-domain",
      { method: "POST", body: JSON.stringify({ new_host: newHost, new_zone_id: zone.cf_zone_id }) },
      120000
    );
    await env.DB.prepare("UPDATE nodes SET vpn_host=?, zone=?, status='active', last_seen_at=? WHERE id=?")
      .bind(out.vpn_host, zone.name, nowTs(), id)
      .run();
    await logEvent(env, actor, "node.rotate", "ok", { old_host: row.vpn_host, new_host: out.vpn_host }, id);
    return json({ new_host: out.vpn_host });
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
    const out = await callAgent<AgentSyncResponse>(
      env,
      row.admin_host,
      "/admin/v1/sync",
      { method: "POST", body: JSON.stringify({ users: body.users }) },
      120000
    );
    await logEvent(env, actor, "node.sync", "ok", out, id);
    return json(out);
  } catch (e) {
    await logEvent(env, actor, "node.sync", "error", { message: String(e) }, id);
    return error(502, { error: "sync_failed", detail: String(e) });
  }
}
