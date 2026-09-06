import type { Env } from "../types";
import { all, one } from "../lib/db";
import { buildSubscriptionURIs, encodeSubscriptionBody, type SubscriptionRow } from "../lib/subscription";

const TOKEN_RE = /^[a-f0-9]{32}$/;

// Profile name shown by clients; also the REMARKS= line inside the body.
const PROFILE_TITLE = "RWL8899";

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
      "SELECT un.vless_uuid, un.hy2_pw, n.vpn_host, un.node_id, n.hy2_host, n.hy2_port, n.hy2_obfs_pw, n.mode, n.reality_pubkey, n.reality_sid, n.reality_sni, n.xhttp_path FROM user_nodes un JOIN nodes n ON n.id=un.node_id WHERE un.user_id=? ORDER BY un.node_id"
    ).bind(user.id)
  );

  const body = encodeSubscriptionBody(buildSubscriptionURIs(user.id, rows), PROFILE_TITLE);
  return new Response(body, {
    status: 200,
    headers: {
      "content-type": "text/plain; charset=utf-8",
      "cache-control": "no-store, private",
      "profile-update-interval": "24",
      // No `subscription-userinfo`: an EMPTY value is not the same as an absent
      // one — Shadowrocket / v2rayN parse it as upload=0, download=0, total=0
      // ("0 B of 0 B"), and some builds read that as an exhausted quota and
      // refuse to auto-update. We have no traffic accounting to report anyway.
      // profile-title names the profile in clients that ignore the REMARKS=
      // line (a Shadowrocket-only convention).
      "profile-title": `base64:${btoa(PROFILE_TITLE)}`
    }
  });
}
