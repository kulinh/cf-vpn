import type { SubscriptionRow } from "./subscription";
import { hasHy2, hasIPv6, hasNaive, hy2Address, isCloudflareRow, isRealityRow, realityHost } from "./subscription";
import { httpUpgradeName, hy2Name, hy2V6Name, naiveName, realityName, realityV6Name, xhttpH3Name } from "./clash";
import { AUTO_MEMBERS } from "./shadowrocket";
import type { ModuleRules } from "./cnrules";

// A complete sing-box configuration (1.12+ syntax: typed DNS servers, rule
// actions, inline rule sets; the naive outbound needs 1.13) for the official
// sing-box apps (SFI on iOS/macOS, SFA on Android). It carries what the base64
// list cannot: the AUTO/PROXY groups and the blocked-site rules, so the phone
// proxies only what the sr_proxy_list module lists and sends the rest direct,
// the same split the Shadowrocket .conf gives.
//
// Hiddify imports the same document but keeps only the outbounds (it rebuilds
// route/dns itself), so it still works there, just without the split.
//
// XHTTP routes are left out: upstream sing-box has no xhttp transport.

export type SingboxFinal = "direct" | "proxy";

export interface SingboxOptions {
  final?: SingboxFinal;
  // Control-plane hosts that must ride the proxy from China (leading dot =
  // whole zone), same meaning as in the Shadowrocket builder.
  alwaysProxyHosts?: string[];
  // Blacklist mode: the parsed module. Required when final is "direct"; the
  // caller refuses to serve a split config without it.
  moduleRules?: ModuleRules | null;
}

type Json = Record<string, unknown>;

const URL_TEST = { url: "http://cp.cloudflare.com/generate_204", interval: "10m", tolerance: 500 };

function nodeOutbounds(username: string, rows: SubscriptionRow[]): Json[] {
  const out: Json[] = [];
  for (const r of rows) {
    if (isRealityRow(r)) {
      const reality = (tag: string, server: string): Json => ({
        type: "vless",
        tag,
        server,
        server_port: 443,
        uuid: r.vless_uuid,
        flow: "xtls-rprx-vision",
        tls: {
          enabled: true,
          server_name: r.reality_sni,
          utls: { enabled: true, fingerprint: "chrome" },
          reality: { enabled: true, public_key: r.reality_pubkey, short_id: r.reality_sid }
        }
      });
      out.push(reality(realityName(username, r.node_id), realityHost(r)));
      // IPv6 twin: PROXY only (AUTO members are fixed IPv4 names, and the
      // HY2-BACKUP filter matches "-HY2" at the end, not "-HY2-v6").
      if (hasIPv6(r)) {
        out.push(reality(realityV6Name(username, r.node_id), r.public_ipv6!));
      }
    } else if (isCloudflareRow(r)) {
      out.push({
        type: "vless",
        tag: httpUpgradeName(username, r.node_id),
        server: r.vpn_host,
        server_port: 443,
        uuid: r.vless_uuid,
        tls: { enabled: true, server_name: r.vpn_host },
        transport: { type: "httpupgrade", host: r.vpn_host, path: r.xhttp_path ?? "/api/v1/sync" }
      });
    } else {
      continue;
    }
    if (hasHy2(r)) {
      const hy2 = (tag: string, server: string): Json => ({
        type: "hysteria2",
        tag,
        server,
        server_port: r.hy2_port,
        // The server runs hysteria `auth.type: userpass`, whose auth string
        // is "user:pass" (the base64 URI carries the same pair).
        password: `${username}:${r.hy2_pw}`,
        obfs: { type: "salamander", password: r.hy2_obfs_pw },
        tls: { enabled: true, server_name: r.hy2_host }
      });
      out.push(hy2(hy2Name(username, r.node_id), hy2Address(r)));
      if (hasIPv6(r)) {
        out.push(hy2(hy2V6Name(username, r.node_id), r.public_ipv6!));
      }
    }
    if (hasNaive(r)) {
      out.push({
        type: "naive",
        tag: naiveName(username, r.node_id),
        server: r.naive_host,
        server_port: 443,
        username: r.naive_user,
        password: r.naive_pass,
        tls: { enabled: true, server_name: r.naive_host }
      });
    }
  }
  return out;
}

// Shadowrocket rule lines ("TYPE,value,PROXY[,no-resolve]") → headless rules.
// Domain and IP matchers go in separate rules so neither list narrows the
// other. Types sing-box cannot express without extra state (USER-AGENT,
// URL-REGEX, GEOIP, IP-ASN) are dropped; the CN module carries none today.
export function moduleToHeadlessRules(rules: string[]): Json[] {
  const domain: string[] = [];
  const suffix: string[] = [];
  const keyword: string[] = [];
  const cidr: string[] = [];
  for (const line of rules) {
    const [type, value] = line.split(",");
    switch (type) {
      case "DOMAIN": domain.push(value); break;
      case "DOMAIN-SUFFIX": suffix.push(value); break;
      case "DOMAIN-KEYWORD": keyword.push(value); break;
      case "IP-CIDR":
      case "IP-CIDR6": cidr.push(value); break;
    }
  }
  const out: Json[] = [];
  const names: Json = {};
  if (domain.length) names.domain = domain;
  if (suffix.length) names.domain_suffix = suffix;
  if (keyword.length) names.domain_keyword = keyword;
  if (Object.keys(names).length) out.push(names);
  if (cidr.length) out.push({ ip_cidr: cidr });
  return out;
}

function alwaysProxyRule(hosts: string[]): Json | null {
  const domain = hosts.filter((h) => !h.startsWith("."));
  const suffix = hosts.filter((h) => h.startsWith(".")).map((h) => h.slice(1));
  if (!domain.length && !suffix.length) return null;
  const rule: Json = {};
  if (domain.length) rule.domain = domain;
  if (suffix.length) rule.domain_suffix = suffix;
  return rule;
}

export function buildSingboxConfig(username: string, rows: SubscriptionRow[], opts: SingboxOptions = {}): Json {
  const final: SingboxFinal = opts.final ?? "direct";
  const nodes = nodeOutbounds(username, rows);
  const tags = nodes.map((o) => o.tag as string);
  const have = new Set(tags);
  // sing-box has no xhttp, so an XHTTP-H3 member of the shared list is taken
  // by the same node's HY2 route instead: also QUIC, and measured on par with
  // H3 from the home line (2026-09-13, VNM-01 -> JPY-03: ~10 MB/s for both,
  // REALITY ~3.5). Shadowrocket keeps H3.
  const auto = [...new Set(AUTO_MEMBERS.map(([id, name]) =>
    name === xhttpH3Name && !have.has(name(username, id)) ? hy2Name(username, id) : name(username, id)
  ))].filter((n) => have.has(n));
  const hy2 = tags.filter((t) => t.endsWith("-HY2"));

  const groups: Json[] = [];
  const proxyMembers: string[] = [];
  if (auto.length > 0) {
    groups.push({ type: "urltest", tag: "AUTO", outbounds: auto, ...URL_TEST });
    proxyMembers.push("AUTO");
  }
  if (hy2.length > 0) {
    groups.push({ type: "selector", tag: "HY2-BACKUP", outbounds: hy2 });
    proxyMembers.push("HY2-BACKUP");
  }
  proxyMembers.push(...tags);
  groups.unshift({ type: "selector", tag: "PROXY", outbounds: proxyMembers.length > 0 ? proxyMembers : ["DIRECT"] });

  const always = alwaysProxyRule(opts.alwaysProxyHosts ?? []);
  const blocked = final === "direct" && opts.moduleRules ? moduleToHeadlessRules(opts.moduleRules.rules) : [];

  const routeRules: Json[] = [
    { action: "sniff" },
    { protocol: "dns", action: "hijack-dns" },
    { ip_is_private: true, action: "route", outbound: "DIRECT" }
  ];
  const dnsRules: Json[] = [];
  const ruleSets: Json[] = [];
  if (always) {
    routeRules.push({ ...always, action: "route", outbound: "PROXY" });
    dnsRules.push({ ...always, action: "route", server: "remote" });
  }
  // Names and addresses go in separate rule sets. A DNS rule that references
  // a rule set with ip_cidr is a "legacy address filter" since sing-box 1.14
  // (deprecated, removed in 1.16), so DNS only ever sees the domain set.
  const blockedDomain = blocked.filter((r) => !("ip_cidr" in r));
  const blockedIP = blocked.filter((r) => "ip_cidr" in r);
  if (blockedDomain.length > 0) ruleSets.push({ type: "inline", tag: "blocked-domain", rules: blockedDomain });
  if (blockedIP.length > 0) ruleSets.push({ type: "inline", tag: "blocked-ip", rules: blockedIP });
  if (ruleSets.length > 0) {
    routeRules.push({ rule_set: ruleSets.map((s) => s.tag), action: "route", outbound: "PROXY" });
  }
  if (blockedDomain.length > 0) {
    // Blocked names are resolved through the tunnel so a poisoned local answer
    // cannot send the connection somewhere else; everything else uses the
    // local resolver, which is what keeps domestic sites fast.
    dnsRules.push({ rule_set: ["blocked-domain"], action: "route", server: "remote" });
  }

  const route: Json = {
    rules: routeRules,
    final: final === "proxy" ? "PROXY" : "DIRECT",
    auto_detect_interface: true,
    default_domain_resolver: "local"
  };
  if (ruleSets.length > 0) route.rule_set = ruleSets;

  return {
    log: { level: "warn" },
    dns: {
      servers: [
        { type: "https", tag: "remote", server: "1.1.1.1", detour: "PROXY" },
        { type: "local", tag: "local" }
      ],
      rules: dnsRules,
      final: final === "proxy" ? "remote" : "local",
      strategy: "prefer_ipv4"
    },
    inbounds: [
      { type: "tun", tag: "tun-in", address: ["172.19.0.1/30", "fdfe:dcba:9876::1/126"], auto_route: true, strict_route: true }
    ],
    outbounds: [...groups, ...nodes, { type: "direct", tag: "DIRECT" }],
    route,
    experimental: { cache_file: { enabled: true } }
  };
}
