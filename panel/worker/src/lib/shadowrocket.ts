import type { SubscriptionRow } from "./subscription";
import { realityName, httpUpgradeName, hy2Name, xhttpName, xhttpDirectName, hasHy2, isCloudflareRow, isRealityRow } from "./clash";
import { hasXHTTP, hasXHTTPDirect } from "./subscription";

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

export function buildShadowrocketConfig(username: string, rows: SubscriptionRow[]): string {
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
  out.push("", "[Rule]", members.length > 0 ? "FINAL,AUTO" : "FINAL,PROXY", "");
  return out.join("\n");
}
