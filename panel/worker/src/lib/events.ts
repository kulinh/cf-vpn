import type { Env } from "../types";
import { nowTs } from "./db";

// Bound INSERT for one audit row, without running it — lets a caller fold event
// writes into the same env.DB.batch() as the row updates they describe (see the
// cron sweep) so a partial failure can't commit the update without the event.
export function eventStatement(
  env: Env,
  actor: string,
  action: string,
  outcome: "ok" | "partial" | "error",
  detail: unknown,
  nodeID?: string,
  userID?: string
): D1PreparedStatement {
  return env.DB.prepare(
    "INSERT INTO events (ts, actor, action, node_id, user_id, outcome, detail) VALUES (?, ?, ?, ?, ?, ?, ?)"
  ).bind(nowTs(), actor, action, nodeID ?? null, userID ?? null, outcome, JSON.stringify(detail));
}

export async function logEvent(
  env: Env,
  actor: string,
  action: string,
  outcome: "ok" | "partial" | "error",
  detail: unknown,
  nodeID?: string,
  userID?: string
): Promise<void> {
  await eventStatement(env, actor, action, outcome, detail, nodeID, userID).run();
}
