import type { Env } from "../types";
import { all, one } from "../lib/db";
import { buildSubscriptionURIs, encodeSubscriptionBody, type SubscriptionRow } from "../lib/subscription";

const TOKEN_RE = /^[a-f0-9]{32}$/;

function notFoundText(): Response {
  return new Response("not found", {
    status: 404,
    headers: { "content-type": "text/plain; charset=utf-8", "cache-control": "no-store" }
  });
}

export async function publicSubscription(env: Env, token: string): Promise<Response> {
  if (!TOKEN_RE.test(token)) {
    return notFoundText();
  }

  const user = await one<{ id: string }>(
    env.DB.prepare("SELECT id FROM users WHERE sub_token=?").bind(token)
  );
  if (!user) {
    return notFoundText();
  }

  const rows = await all<SubscriptionRow>(
    env.DB.prepare(
      "SELECT un.vless_uuid, un.hy2_pw, n.vpn_host, un.node_id, n.hy2_host, n.hy2_port, n.hy2_obfs_pw FROM user_nodes un JOIN nodes n ON n.id=un.node_id WHERE un.user_id=? ORDER BY un.node_id"
    ).bind(user.id)
  );

  const body = encodeSubscriptionBody(buildSubscriptionURIs(user.id, rows), "RWL8899");
  return new Response(body, {
    status: 200,
    headers: {
      "content-type": "text/plain; charset=utf-8",
      "cache-control": "no-store, private",
      "profile-update-interval": "24",
      "subscription-userinfo": ""
    }
  });
}
