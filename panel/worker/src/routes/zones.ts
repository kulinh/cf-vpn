import type { Env } from "../types";
import { all, nowTs, one } from "../lib/db";
import { error, isRecord, json, readJSON } from "../lib/http";
import { logEvent } from "../lib/events";
import { isCfId } from "../lib/cf-api";

export async function listZones(env: Env): Promise<Response> {
  const rows = await all<{ name: string; cf_zone_id: string; enabled: number }>(
    env.DB.prepare("SELECT name,cf_zone_id,enabled FROM zones ORDER BY name")
  );
  return json(rows);
}

export async function createZone(env: Env, request: Request, actor = "system"): Promise<Response> {
  let body: { name: string; cf_zone_id: string; enabled?: number };
  try {
    body = await readJSON<{ name: string; cf_zone_id: string; enabled?: number }>(request);
  } catch {
    return error(400, { error: "invalid_json", detail: "request body must be valid JSON" });
  }
  if (!isRecord(body)) {
    return error(400, { error: "invalid_zone", detail: "request body must be a JSON object" });
  }
  if (!body.name || !body.cf_zone_id) {
    return error(400, { error: "invalid_zone", detail: "name and cf_zone_id are required" });
  }
  // cf_zone_id is interpolated into Cloudflare API paths by lib/cf-api.ts —
  // reject anything that is not a 32-hex zone id at the write boundary.
  if (!isCfId(body.cf_zone_id)) {
    return error(400, { error: "invalid_cf_zone_id", detail: "cf_zone_id must be 32 hex characters" });
  }
  const exists = await one<{ name: string }>(env.DB.prepare("SELECT name FROM zones WHERE name = ?").bind(body.name));
  if (exists) {
    return error(409, { error: "zone_exists", detail: body.name });
  }

  await env.DB.prepare("INSERT INTO zones (name,cf_zone_id,enabled,created_at) VALUES (?, ?, ?, ?)")
    .bind(body.name, body.cf_zone_id, body.enabled ?? 1, nowTs())
    .run();
  await logEvent(env, actor, "zone.create", "ok", { name: body.name, cf_zone_id: body.cf_zone_id, enabled: body.enabled ?? 1 });
  return json({ ok: true }, 201);
}

export async function patchZone(env: Env, name: string, request: Request, actor = "system"): Promise<Response> {
  // One SELECT for all three reads: the previous code re-read cf_zone_id and
  // enabled in separate queries, which could bind `undefined` if the row was
  // deleted in between (and cost 3 round-trips for one row).
  const existing = await one<{ name: string; cf_zone_id: string; enabled: number }>(
    env.DB.prepare("SELECT name, cf_zone_id, enabled FROM zones WHERE name = ?").bind(name)
  );
  if (!existing) {
    return error(404, { error: "zone_not_found", detail: name });
  }
  let body: { cf_zone_id?: string; enabled?: number };
  try {
    body = await readJSON<{ cf_zone_id?: string; enabled?: number }>(request);
  } catch {
    return error(400, { error: "invalid_json", detail: "request body must be valid JSON" });
  }
  if (!isRecord(body)) {
    return error(400, { error: "invalid_zone", detail: "request body must be a JSON object" });
  }
  if (body.cf_zone_id !== undefined && !isCfId(body.cf_zone_id)) {
    return error(400, { error: "invalid_cf_zone_id", detail: "cf_zone_id must be 32 hex characters" });
  }
  const nextZoneID = body.cf_zone_id ?? existing.cf_zone_id;
  const nextEnabled = body.enabled ?? existing.enabled;
  await env.DB.prepare("UPDATE zones SET cf_zone_id=?, enabled=? WHERE name=?")
    .bind(nextZoneID, nextEnabled, name)
    .run();
  await logEvent(env, actor, "zone.update", "ok", { name, cf_zone_id: nextZoneID, enabled: nextEnabled });
  return json({ ok: true });
}

export async function deleteZone(env: Env, name: string, actor = "system"): Promise<Response> {
  // Refuse to delete a zone still referenced by nodes — orphaning nodes on a
  // missing zone breaks rotate and DNS cleanup.
  const inUse = await one<{ n: number }>(
    env.DB.prepare("SELECT COUNT(*) AS n FROM nodes WHERE zone = ?").bind(name)
  );
  if (inUse && inUse.n > 0) {
    return error(409, { error: "zone_in_use", detail: `${inUse.n} node(s) still reference zone ${name}` });
  }
  await env.DB.prepare("DELETE FROM zones WHERE name=?").bind(name).run();
  await logEvent(env, actor, "zone.delete", "ok", { name });
  return json({ ok: true });
}
