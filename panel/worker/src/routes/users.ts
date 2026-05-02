import type { AgentAddUserResponse, Env } from "../types";
import { all, nowTs, one, randomHex, userIDFromName } from "../lib/db";
import { callAgent } from "../lib/agent-client";
import { error, isRecord, json, readJSON } from "../lib/http";
import { logEvent } from "../lib/events";
import { buildSubscriptionForClient } from "../lib/subscription";

interface NodeMini {
  id: string;
  admin_host: string;
  agent_secret: string | null;
}

interface UserWithNodes {
  id: string;
  name: string;
  created_at: number;
  nodes: string[];
}

export async function listUsers(env: Env): Promise<Response> {
  const users = await all<{ id: string; name: string; created_at: number }>(
    env.DB.prepare("SELECT id,name,created_at FROM users ORDER BY id")
  );
  const rows = await all<{ user_id: string; node_id: string }>(
    env.DB.prepare("SELECT user_id,node_id FROM user_nodes ORDER BY user_id,node_id")
  );

  const byUser = new Map<string, string[]>();
  for (const row of rows) {
    const items = byUser.get(row.user_id) ?? [];
    items.push(row.node_id);
    byUser.set(row.user_id, items);
  }

  const out: UserWithNodes[] = users.map((u) => ({
    ...u,
    nodes: byUser.get(u.id) ?? []
  }));
  return json(out);
}

export async function createUser(env: Env, request: Request, actor: string): Promise<Response> {
  let body: { name: string };
  try {
    body = await readJSON<{ name: string }>(request);
  } catch {
    return error(400, { error: "invalid_json", detail: "request body must be valid JSON" });
  }
  if (!isRecord(body)) {
    return error(400, { error: "invalid_user", detail: "request body must be a JSON object" });
  }
  const name = body.name?.trim();
  if (!name) {
    return error(400, { error: "invalid_user", detail: "name is required" });
  }
  const id = userIDFromName(name);
  if (!id) {
    return error(400, { error: "invalid_user", detail: "name is invalid" });
  }

  const existing = await one<{ id: string }>(env.DB.prepare("SELECT id FROM users WHERE id=?").bind(id));
  if (existing) {
    return error(409, { error: "user_exists", detail: id });
  }

  const subToken = randomHex(16);
  await env.DB.prepare("INSERT INTO users (id,name,created_at,sub_token) VALUES (?, ?, ?, ?)")
    .bind(id, name, nowTs(), subToken)
    .run();
  const nodes = await all<NodeMini>(
    env.DB.prepare("SELECT id,admin_host,agent_secret FROM nodes WHERE status='active' ORDER BY id")
  );

  const results = await Promise.allSettled(
    nodes.map(async (node) => {
      const creds = await callAgent<AgentAddUserResponse>(
        env,
        { adminHost: node.admin_host, agentSecret: node.agent_secret },
        "/admin/v1/users",
        { method: "POST", body: JSON.stringify({ name: id }) },
        120000
      );
      await env.DB.prepare(
        "INSERT OR REPLACE INTO user_nodes (user_id,node_id,vless_uuid,hy2_pw,created_at) VALUES (?, ?, ?, ?, ?)"
      )
        .bind(id, node.id, creds.vless_uuid, creds.hy2_pw, nowTs())
        .run();
      return { node_id: node.id, ok: true };
    })
  );

  const summary = results.map((r, i) => {
    if (r.status === "fulfilled") {
      return r.value;
    }
    return { node_id: nodes[i]?.id, ok: false, error: String(r.reason) };
  });
  const failed = summary.some((x) => !x.ok);
  const succeeded = summary.some((x) => x.ok);
  const outcome = failed ? (succeeded ? "partial" : "error") : "ok";
  await logEvent(env, actor, "user.add", outcome, { user_id: id, results: summary }, undefined, id);

  return json({ id, name, results: summary }, failed ? 207 : 201);
}

export async function deleteUser(env: Env, id: string, actor: string): Promise<Response> {
  const user = await one<{ id: string; name: string }>(env.DB.prepare("SELECT id,name FROM users WHERE id=?").bind(id));
  if (!user) {
    return error(404, { error: "user_not_found", detail: id });
  }

  // Target every active node, not only nodes already in user_nodes. The agent
  // accepts DELETE on a missing user as 404 / no-op, so re-targeting is safe
  // and ensures partial-failure retries reach nodes that were skipped on the
  // first try (e.g. a node that came back online after the previous attempt).
  const nodes = await all<NodeMini>(
    env.DB.prepare(
      "SELECT id,admin_host,agent_secret FROM nodes WHERE id IN (SELECT node_id FROM user_nodes WHERE user_id = ?) OR status='active' ORDER BY id"
    ).bind(id)
  );

  const results = await Promise.allSettled(
    nodes.map(async (node) => {
      try {
        await callAgent(
          env,
          { adminHost: node.admin_host, agentSecret: node.agent_secret },
          `/admin/v1/users/${encodeURIComponent(id)}`,
          { method: "DELETE" },
          120000
        );
      } catch (e) {
        // Treat 404 as success — the user is already absent on that node.
        if (!String(e).includes("user_not_found") && !String(e).includes("agent_http_404")) {
          throw e;
        }
      }
      await env.DB.prepare("DELETE FROM user_nodes WHERE user_id = ? AND node_id = ?").bind(id, node.id).run();
      return { node_id: node.id, ok: true };
    })
  );

  const summary = results.map((r, i) => {
    if (r.status === "fulfilled") {
      return r.value;
    }
    return { node_id: nodes[i]?.id, ok: false, error: String(r.reason) };
  });
  const failed = summary.some((x) => !x.ok);
  const succeeded = summary.some((x) => x.ok);
  const outcome = failed ? (succeeded ? "partial" : "error") : "ok";
  if (!failed) {
    await env.DB.prepare("DELETE FROM users WHERE id = ?").bind(id).run();
  }

  await logEvent(env, actor, "user.remove", outcome, { user_id: id, results: summary }, undefined, id);
  return json({ ok: !failed, results: summary }, failed ? 207 : 200);
}

export async function userUpgradeNodes(env: Env, id: string, actor: string): Promise<Response> {
  const user = await one<{ id: string; name: string }>(env.DB.prepare("SELECT id,name FROM users WHERE id=?").bind(id));
  if (!user) {
    return error(404, { error: "user_not_found", detail: id });
  }

  const activeNodes = await all<NodeMini>(
    env.DB.prepare("SELECT id,admin_host,agent_secret FROM nodes WHERE status='active' ORDER BY id")
  );
  const existingRows = await all<{ node_id: string }>(
    env.DB.prepare("SELECT node_id FROM user_nodes WHERE user_id=?").bind(id)
  );
  const existingNodeIds = new Set(existingRows.map((r) => r.node_id));

  const nodesToAdd = activeNodes.filter((n) => !existingNodeIds.has(n.id));
  const addedNodes: string[] = [];

  const results = await Promise.allSettled(
    nodesToAdd.map(async (node) => {
      const creds = await callAgent<AgentAddUserResponse>(
        env,
        { adminHost: node.admin_host, agentSecret: node.agent_secret },
        "/admin/v1/users",
        { method: "POST", body: JSON.stringify({ name: id }) },
        120000
      );
      await env.DB.prepare(
        "INSERT OR REPLACE INTO user_nodes (user_id,node_id,vless_uuid,hy2_pw,created_at) VALUES (?, ?, ?, ?, ?)"
      )
        .bind(id, node.id, creds.vless_uuid, creds.hy2_pw, nowTs())
        .run();
      addedNodes.push(node.id);
      return { node_id: node.id, ok: true };
    })
  );

  const summary = results.map((r, i) => {
    if (r.status === "fulfilled") return r.value;
    return { node_id: nodesToAdd[i]?.id, ok: false, error: String(r.reason) };
  });
  const failedCount = summary.filter((x) => !x.ok).length;
  const failed = failedCount > 0;
  const succeeded = summary.some((x) => x.ok);
  const outcome = failed ? (succeeded ? "partial" : "error") : "ok";

  await logEvent(
    env,
    actor,
    "user.upgrade_nodes",
    outcome,
    { user_id: id, added: addedNodes, results: summary },
    undefined,
    id
  );

  if (failed && !succeeded) {
    return error(502, {
      error: "upgrade_failed",
      detail: "failed to add user to all missing nodes",
      userId: id,
      addedNodes,
      addedCount: addedNodes.length,
      failedCount,
      alreadyPresentCount: existingNodeIds.size,
      totalNodesAfterUpgrade: existingNodeIds.size + addedNodes.length,
      results: summary
    });
  }

  const status = failed ? 207 : 200;
  return json(
    {
      userId: id,
      addedNodes,
      addedCount: addedNodes.length,
      failedCount,
      alreadyPresentCount: existingNodeIds.size,
      totalNodesAfterUpgrade: existingNodeIds.size + addedNodes.length,
      results: summary
    },
    status
  );
}

export async function userSubscription(env: Env, id: string): Promise<Response> {
  const user = await one<{ id: string; name: string; sub_token: string | null }>(
    env.DB.prepare("SELECT id,name,sub_token FROM users WHERE id=?").bind(id)
  );
  if (!user) {
    return error(404, { error: "user_not_found", detail: id });
  }

  let subToken = user.sub_token;
  if (!subToken) {
    subToken = randomHex(16);
    await env.DB.prepare("UPDATE users SET sub_token=? WHERE id=?").bind(subToken, id).run();
  }

  const rows = await all<{ vless_uuid: string; hy2_pw: string; vpn_host: string; node_id: string; hy2_host: string | null; hy2_port: number | null; hy2_obfs_pw: string | null; mode: string | null; reality_pubkey: string | null; reality_sid: string | null; reality_sni: string | null; xhttp_path: string | null }>(
    env.DB.prepare(
      "SELECT un.vless_uuid, un.hy2_pw, n.vpn_host, un.node_id, n.hy2_host, n.hy2_port, n.hy2_obfs_pw, n.mode, n.reality_pubkey, n.reality_sid, n.reality_sni, n.xhttp_path FROM user_nodes un JOIN nodes n ON n.id=un.node_id WHERE un.user_id=? ORDER BY un.node_id"
    ).bind(id)
  );
  const out = buildSubscriptionForClient(user.id, rows);
  return json({ ...out, sub_token: subToken });
}
