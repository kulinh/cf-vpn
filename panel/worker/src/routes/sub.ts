import type { Env } from "../types";
import { all, one } from "../lib/db";
import { buildSubscriptionURIs, encodeSubscriptionBody, type SubscriptionRow } from "../lib/subscription";
import { buildClashConfig } from "../lib/clash";
import { buildShadowrocketConfig } from "../lib/shadowrocket";
import { fetchModuleRules, isRuleSetKey, moduleURL } from "../lib/cnrules";
import { RULES_MODE_KEY, getSetting } from "../lib/settings";
import { error } from "../lib/http";

const TOKEN_RE = /^[a-f0-9]{32}$/;

// Profile name shown by clients; also the REMARKS= line inside the body.
const PROFILE_TITLE = "RWL8899";

function notFoundText(): Response {
  return new Response("not found", {
    status: 404,
    headers: { "content-type": "text/plain; charset=utf-8", "cache-control": "no-store" }
  });
}

export async function publicSubscription(
  env: Env,
  token: string,
  format?: string | null,
  final?: string | null,
  rules?: string | null
): Promise<Response> {
  if (!TOKEN_RE.test(token)) {
    return notFoundText();
  }
  // Unknown formats are rejected rather than silently served as base64 — a
  // typo'd ?format= would otherwise hand a Clash client a base64 blob it cannot
  // parse, with no clue why.
  const wantsClash = format === "clash";
  const wantsShadowrocket = format === "shadowrocket";
  if (format != null && format !== "" && !wantsClash && !wantsShadowrocket) {
    return error(400, { error: "invalid_format", detail: "supported: clash, shadowrocket (omit for base64)" });
  }
  // ?final= only means something for the Shadowrocket .conf; reject typos
  // for the same reason as ?format=.
  if (final != null && final !== "" && final !== "direct" && final !== "proxy") {
    return error(400, { error: "invalid_final", detail: "supported: direct (default, blacklist mode with the CN module) or proxy (full tunnel)" });
  }
  // ?rules=cn (default) inlines the sr_proxy_list_CN module into the .conf,
  // ?rules=uae the UAE one; ?rules=none leaves the [Rule] section bare for
  // users who load a module themselves.
  if (rules != null && rules !== "" && rules !== "none" && !isRuleSetKey(rules)) {
    return error(400, { error: "invalid_rules", detail: "supported: cn (default, inline sr_proxy_list_CN), uae (inline sr_proxy_list_UAE) or none" });
  }

  const user = await one<{ id: string }>(
    env.DB.prepare("SELECT id FROM users WHERE sub_token=?").bind(token)
  );
  if (!user) {
    return notFoundText();
  }

  const rows = await all<SubscriptionRow>(
    env.DB.prepare(
      "SELECT un.vless_uuid, un.hy2_pw, n.vpn_host, n.public_ip, un.node_id, n.hy2_host, n.hy2_port, n.hy2_obfs_pw, n.mode, n.reality_pubkey, n.reality_sid, n.reality_sni, n.xhttp_path, n.xhttp_enabled, n.xhttp_direct_host, n.xhttp_direct_path, n.xhttp_h3_host, n.xhttp_h3_path FROM user_nodes un JOIN nodes n ON n.id=un.node_id WHERE un.user_id=? ORDER BY un.node_id"
    ).bind(user.id)
  );

  if (wantsShadowrocket) {
    // A Shadowrocket .conf: policy groups + rules only. The nodes themselves
    // come from the base64 subscription, whose names the groups reference.
    // The panel itself sits behind Cloudflare Access; from China both the
    // subscription refresh and the Access login must go through the proxy.
    const alwaysProxyHosts: string[] = [".cloudflareaccess.com"];
    try {
      if (env.PANEL_PUBLIC_ORIGIN) alwaysProxyHosts.unshift(new URL(env.PANEL_PUBLIC_ORIGIN).hostname);
    } catch {
      // a malformed origin just means no panel rule
    }
    // Blacklist mode pulls the blocked-site module at the edge (GitHub is
    // reachable from Cloudflare, not from China) and inlines it. Full tunnel
    // has no use for it.
    // No ?rules= on the link: use the fleet-wide travel mode the operator set
    // (Telegram /mode or `cfvpnctl rules-mode`), so an installed config link
    // follows the trip without being edited. An explicit ?rules= always wins.
    let effective: string | null = rules ? rules : null;
    if (effective == null && final !== "proxy") {
      const stored = await getSetting(env, RULES_MODE_KEY);
      effective = stored && (stored === "none" || isRuleSetKey(stored)) ? stored : "cn";
    }
    const wantsRules = final !== "proxy" && effective !== "none";
    const source = moduleURL(effective && isRuleSetKey(effective) ? effective : "cn", env.RULES_BASE_URL || undefined);
    const moduleRules = wantsRules ? await fetchModuleRules(source) : undefined;
    const conf = buildShadowrocketConfig(user.id, rows, {
      final: final === "proxy" ? "proxy" : "direct",
      alwaysProxyHosts,
      moduleRules,
      // The RULE-SET fallback wants a plain rule list, which the module repo
      // publishes next to each module.
      moduleFallbackURL: wantsRules ? source.replace(/\.module$/, ".list") : undefined
    });
    return new Response(conf, {
      status: 200,
      headers: {
        "content-type": "text/plain; charset=utf-8",
        "content-disposition": `attachment; filename="${PROFILE_TITLE}.conf"`,
        "cache-control": "no-store, private"
      }
    });
  }

  if (wantsClash) {
    return new Response(buildClashConfig(user.id, rows), {
      status: 200,
      headers: {
        "content-type": "text/yaml; charset=utf-8",
        "cache-control": "no-store, private",
        "profile-update-interval": "24",
        "profile-title": `base64:${btoa(PROFILE_TITLE)}`
      }
    });
  }

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
