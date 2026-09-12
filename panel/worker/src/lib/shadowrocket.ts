import type { SubscriptionRow } from "./subscription";
import { realityName, httpUpgradeName, hy2Name, xhttpName, xhttpDirectName, hasHy2, isCloudflareRow, isRealityRow } from "./clash";
import { hasXHTTP, hasXHTTPDirect } from "./subscription";
import type { ModuleRules } from "./cnrules";

// Shadowrocket ".conf" companion to the base64 subscription. The subscription
// carries the nodes; this file carries the policy groups and rules that the
// base64 format cannot express. Group members are referenced by the exact
// node names the subscription emits, so the user imports both and the group
// resolves against the subscription's nodes.

// AUTO membership is an operator decision (2026-09-12): the two primary
// Reality nodes, the two nodes that keep Hysteria2, and the one cloudflare
// route that stayed stable from China. Members the user does not have are
// skipped so the group is always valid.
const AUTO_MEMBERS: ReadonlyArray<readonly [string, (u: string, n: string) => string]> = [
  ["JPY-02", realityName],
  ["SIN-01", realityName],
  // JPY-03 (Oracle Osaka, added 2026-09-12): a Japan route on a different
  // provider than JPY-01/JPY-02, so a GreenCloud problem cannot take every
  // Japanese member of the group with it.
  ["JPY-03", realityName],
  ["JPY-01", hy2Name],
  ["HKG-01", hy2Name],
  ["OR-001", httpUpgradeName]
];

export const AUTO_URL_TEST_OPTS =
  "url = http://cp.cloudflare.com/generate_204, interval = 600, tolerance = 500, timeout = 8";

// Same gating as buildSubscriptionURIs / buildProxies, so the group never
// names a node the subscription does not contain.
export function availableNames(username: string, rows: SubscriptionRow[]): string[] {
  const names: string[] = [];
  for (const r of rows) {
    if (isRealityRow(r)) {
      names.push(realityName(username, r.node_id));
    } else if (isCloudflareRow(r)) {
      names.push(httpUpgradeName(username, r.node_id));
      if (hasXHTTP(r)) {
        names.push(xhttpName(username, r.node_id));
      }
      // Direct route: a standalone backup node in PROXY, never in AUTO.
      if (hasXHTTPDirect(r)) {
        names.push(xhttpDirectName(username, r.node_id));
      }
    } else {
      continue;
    }
    if (hasHy2(r)) {
      names.push(hy2Name(username, r.node_id));
    }
  }
  return names;
}

// How the config's own [Rule] section ends. "direct" is blacklist mode: the
// sr_proxy_list_CN module (loaded above the config) decides what goes to
// PROXY and everything else — Chinese and Vietnamese sites included — goes
// direct; the right mode inside China. "proxy" sends everything through the
// PROXY group (full tunnel), for use at home.
export type ShadowrocketFinal = "direct" | "proxy";

export interface ShadowrocketOptions {
  final?: ShadowrocketFinal;
  // Hostnames of our own control plane that must ride the proxy from China
  // (the panel behind Cloudflare Access, its login page). Emitted as DOMAIN /
  // DOMAIN-SUFFIX rules ahead of FINAL.
  alwaysProxyHosts?: string[];
  // Blacklist mode only. The blocked-site rules pulled from the public
  // sr_proxy_list_<CN|UAE> module, inlined ahead of FINAL so the phone needs
  // no module and never has to reach GitHub. null/undefined with a fallback
  // URL set means the edge fetch failed: emit a RULE-SET line that points
  // Shadowrocket at the list itself. Neither set: the user loads a module by
  // hand (the pre-2026-09-12 behaviour).
  moduleRules?: ModuleRules | null;
  moduleFallbackURL?: string;
}

export function buildShadowrocketConfig(username: string, rows: SubscriptionRow[], opts: ShadowrocketOptions = {}): string {
  const final: ShadowrocketFinal = opts.final ?? "direct";
  const all = availableNames(username, rows);
  const have = new Set(all);
  const members = AUTO_MEMBERS.map(([id, name]) => name(username, id)).filter((n) => have.has(n));

  const out: string[] = [
    "[General]",
    "bypass-system = true",
    "skip-proxy = 192.168.0.0/16, 10.0.0.0/8, 172.16.0.0/12, localhost, *.local",
    "dns-server = system",
    "",
    "[Proxy Group]"
  ];
  if (members.length > 0) {
    out.push(`AUTO = url-test, ${members.join(", ")}, ${AUTO_URL_TEST_OPTS}`);
  }
  // HY2-BACKUP: every Hysteria2 route the user has, as a manual pick for when
  // TCP 443 is throttled but UDP still flows (operator decision 2026-09-12).
  const hy2 = all.filter((n) => n.endsWith("-HY2"));
  if (hy2.length > 0) {
    out.push(`HY2-BACKUP = select, ${hy2.join(", ")}`);
  }
  if (all.length > 0) {
    const groups = [members.length > 0 ? "AUTO" : "", hy2.length > 0 ? "HY2-BACKUP" : ""].filter(Boolean);
    out.push(`PROXY = select, ${groups.length > 0 ? groups.join(", ") + ", " : ""}${all.join(", ")}`);
  } else {
    out.push("PROXY = select, DIRECT");
  }
  out.push("", "[Rule]");
  for (const h of opts.alwaysProxyHosts ?? []) {
    // A bare hostname matches exactly; a leading dot means the whole zone.
    out.push(h.startsWith(".") ? `DOMAIN-SUFFIX,${h.slice(1)},PROXY` : `DOMAIN,${h},PROXY`);
  }
  if (final === "proxy") {
    out.push("# Full tunnel: everything not matched above goes through the PROXY group.", "FINAL,PROXY");
  } else if (opts.moduleRules && opts.moduleRules.rules.length > 0) {
    out.push(
      "# --- Blocked-site list, inlined from the public module kulinh/shadowrocket-vietnamese.",
      "# --- Edit it there, not here: the next config refresh picks the change up.",
      opts.moduleRules.comment,
      ...opts.moduleRules.rules,
      "# --- end of inlined module ---",
      "# Blacklist mode: everything not matched above (Chinese and Vietnamese sites) goes direct.",
      "FINAL,DIRECT"
    );
  } else if (opts.moduleFallbackURL) {
    out.push(
      "# The blocked-site module could not be fetched at the edge just now, so Shadowrocket",
      "# pulls the list itself (needs GitHub reachable). Refresh this config once more later",
      "# to get the rules inlined again.",
      `RULE-SET,${opts.moduleFallbackURL},PROXY`,
      "# Blacklist mode: everything not matched above (Chinese and Vietnamese sites) goes direct.",
      "FINAL,DIRECT"
    );
  } else {
    out.push(
      "# Blacklist mode: load the sr_proxy_list_CN (or _UAE) module above this config; it decides",
      "# what goes to PROXY. Everything else (Chinese and Vietnamese sites) goes direct.",
      "FINAL,DIRECT"
    );
  }
  out.push("");
  return out.join("\n");
}
