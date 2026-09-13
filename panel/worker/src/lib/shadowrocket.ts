import type { SubscriptionRow } from "./subscription";
import { realityName, realityV6Name, httpUpgradeName, hy2Name, hy2V6Name, xhttpName, xhttpDirectName, xhttpH3Name, hasHy2, isCloudflareRow, isRealityRow } from "./clash";
import { hasIPv6, hasXHTTP, hasXHTTPDirect, hasXHTTPH3 } from "./subscription";
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
export const AUTO_MEMBERS: ReadonlyArray<readonly [string, (n: string) => string]> = [
  ["JPY-02", realityName],
  ["SIN-01", realityName],
  // JPY-03 (Oracle Osaka, added 2026-09-12): a Japan route on a different
  // provider than JPY-01/JPY-02, so a GreenCloud problem cannot take every
  // Japanese member of the group with it.
  //
  // Its XHTTP-over-H3 route (2026-09-13) goes first: measured from VNM-01 on
  // the home VNPT line it ran 2-3x faster than the same node's REALITY route
  // on both throughput and TTFB, QUIC being immune to the TCP head-of-line
  // blocking that the lossy VN->APAC path inflicts. REALITY stays in the group
  // right behind it as the fallback for when UDP 443 is throttled.
  ["JPY-03", xhttpH3Name],
  ["JPY-03", realityName],
  ["JPY-01", hy2Name],
  ["HKG-01", hy2Name],
  ["OR-001", httpUpgradeName]
];

// Russia (2026): TSPU throttles QUIC/UDP on most mobile networks and freezes
// TLS to Cloudflare and western hosting after ~16 KB, so the RU profile's
// AUTO is REALITY on the Vietnamese / Singapore / Japan / HK nodes only —
// no HY2, no H3, no Cloudflare-fronted route. Everything else stays a manual
// pick in PROXY. Evidence: ntc.party 16061/20340, Cloudflare blog 2026-07,
// globalping from 7 RU networks 2026-09-13 (TCP 443 to every node fine).
export const AUTO_MEMBERS_RU: ReadonlyArray<readonly [string, (n: string) => string]> = [
  ["HAN-01", realityName],
  ["VNM-02", realityName],
  ["SIN-01", realityName],
  ["JPY-02", realityName],
  ["HKG-01", realityName]
];

// autoMembers picks the AUTO list for a rule set: RU has its own, everything
// else shares AUTO_MEMBERS.
export function autoMembers(rules?: string): ReadonlyArray<readonly [string, (n: string) => string]> {
  return rules === "ru" ? AUTO_MEMBERS_RU : AUTO_MEMBERS;
}

export const AUTO_URL_TEST_OPTS =
  "url = http://cp.cloudflare.com/generate_204, interval = 600, tolerance = 500, timeout = 8";

// Same gating as buildSubscriptionURIs / buildProxies, so the group never
// names a node the subscription does not contain.
export function availableNames(username: string, rows: SubscriptionRow[]): string[] {
  const names: string[] = [];
  for (const r of rows) {
    if (isRealityRow(r)) {
      names.push(realityName(r.node_id));
      // IPv6 twin: PROXY only. It ends in -v6, so the HY2-BACKUP filter below
      // and AUTO (fixed IPv4 members) never pick it up.
      if (hasIPv6(r)) {
        names.push(realityV6Name(r.node_id));
      }
      // Same inbound-level gate as buildSubscriptionURIs: H3 is independent of
      // REALITY and lives on the same direct node.
      if (hasXHTTPH3(r)) {
        names.push(xhttpH3Name(r.node_id));
      }
    } else if (isCloudflareRow(r)) {
      names.push(httpUpgradeName(r.node_id));
      if (hasXHTTP(r)) {
        names.push(xhttpName(r.node_id));
      }
      // Direct route: a standalone backup node in PROXY, never in AUTO.
      if (hasXHTTPDirect(r)) {
        names.push(xhttpDirectName(r.node_id));
      }
    } else {
      continue;
    }
    if (hasHy2(r)) {
      names.push(hy2Name(r.node_id));
      if (hasIPv6(r)) {
        names.push(hy2V6Name(r.node_id));
      }
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
  // Which blocked-site list the config is built for (cn | uae | ru | none);
  // only "ru" changes the AUTO membership.
  rules?: string;
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
  const members = autoMembers(opts.rules).map(([id, name]) => name(id)).filter((n) => have.has(n));

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
