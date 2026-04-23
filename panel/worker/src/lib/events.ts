import type { Env } from "../types";
import { nowTs } from "./db";

export async function logEvent(
  env: Env,
  actor: string,
  action: string,
  outcome: "ok" | "partial" | "error",
  detail: unknown,
  nodeID?: string,
  userID?: string
): Promise<void> {
  await env.DB.prepare(
    "INSERT INTO events (ts, actor, action, node_id, user_id, outcome, detail) VALUES (?, ?, ?, ?, ?, ?, ?)"
  )
    .bind(nowTs(), actor, action, nodeID ?? null, userID ?? null, outcome, JSON.stringify(detail))
    .run();
}
