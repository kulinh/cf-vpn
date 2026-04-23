import type { Env } from "../types";
import { all } from "../lib/db";
import { json } from "../lib/http";

export async function listEvents(env: Env, url: URL): Promise<Response> {
  const raw = Number(url.searchParams.get("limit") ?? "200");
  const limit = Number.isFinite(raw) ? Math.max(1, Math.min(500, Math.trunc(raw))) : 200;
  const rows = await all<{
    id: number;
    ts: number;
    actor: string;
    action: string;
    node_id: string | null;
    user_id: string | null;
    outcome: string;
    detail: string | null;
  }>(
    env.DB.prepare(
      "SELECT id,ts,actor,action,node_id,user_id,outcome,detail FROM events ORDER BY ts DESC LIMIT ?"
    ).bind(limit)
  );
  return json(rows);
}
