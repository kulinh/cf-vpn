import type { SubscriptionRow } from "./subscription";

// Minimal YAML emitter. mihomo/Clash configs are a fixed shape here, so a full
// YAML library would be a dependency for no benefit. Every string is
// double-quoted (so ":", "#", "@" and leading digits are always safe) with only
// the two escapes double-quoted YAML requires; booleans and numbers go bare.
export function yamlString(value: string): string {
  return `"${value.replace(/\\/g, "\\\\").replace(/"/g, '\\"')}"`;
}

type Scalar = string | number | boolean;
type Node = { [key: string]: Scalar | Node | Scalar[] };

function emit(node: Node, indent: string, out: string[]): void {
  for (const [key, value] of Object.entries(node)) {
    if (Array.isArray(value)) {
      if (value.length === 0) {
        out.push(`${indent}${key}: []`);
        continue;
      }
      out.push(`${indent}${key}:`);
      for (const item of value) {
        out.push(`${indent}  - ${scalar(item)}`);
      }
      continue;
    }
    if (typeof value === "object") {
      out.push(`${indent}${key}:`);
      emit(value, `${indent}  `, out);
      continue;
    }
    out.push(`${indent}${key}: ${scalar(value)}`);
  }
}

function scalar(value: Scalar): string {
  return typeof value === "string" ? yamlString(value) : String(value);
}

function emitListItem(node: Node, indent: string, out: string[]): void {
  const lines: string[] = [];
  emit(node, "", lines);
  lines.forEach((line, i) => {
    out.push(i === 0 ? `${indent}- ${line}` : `${indent}  ${line}`);
  });
}

// The proxy names are exactly the fragments of the base64 subscription's URIs,
// so a user sees the same node names in Clash and in Shadowrocket.
export function realityName(username: string, nodeId: string): string {
  return `${username}@${nodeId}-Reality`;
}
export function httpUpgradeName(username: string, nodeId: string): string {
  return `${username}@${nodeId}-HTTPUpgrade`;
}
export function hy2Name(username: string, nodeId: string): string {
  return `${username}@${nodeId}-HY2`;
}

const AUTO_GROUP = "Auto";
const SELECT_GROUP = "Proxy";

function buildProxies(username: string, rows: SubscriptionRow[]): Node[] {
  const proxies: Node[] = [];
  for (const r of rows) {
    // Same gating as buildSubscriptionURIs, so the two formats never disagree
    // about which nodes a user has.
    if (r.mode === "direct" && r.reality_pubkey && r.reality_sid && r.reality_sni) {
      proxies.push({
        name: realityName(username, r.node_id),
        type: "vless",
        server: r.vpn_host,
        port: 443,
        uuid: r.vless_uuid,
        network: "tcp",
        tls: true,
        udp: true,
        flow: "xtls-rprx-vision",
        servername: r.reality_sni,
        "client-fingerprint": "chrome",
        "reality-opts": {
          "public-key": r.reality_pubkey,
          "short-id": r.reality_sid
        }
      });
    } else if (r.mode === "cloudflare") {
      const path = r.xhttp_path ?? "/api/v1/sync";
      proxies.push({
        name: httpUpgradeName(username, r.node_id),
        type: "vless",
        server: r.vpn_host,
        port: 443,
        uuid: r.vless_uuid,
        tls: true,
        udp: true,
        servername: r.vpn_host,
        // mihomo expresses HTTPUpgrade as ws with v2ray-http-upgrade.
        network: "ws",
        "ws-opts": {
          path,
          headers: { Host: r.vpn_host },
          "v2ray-http-upgrade": true
        }
      });
    } else {
      continue;
    }
    if (r.hy2_host && r.hy2_port && r.hy2_obfs_pw) {
      proxies.push({
        name: hy2Name(username, r.node_id),
        type: "hysteria2",
        server: r.hy2_host,
        port: r.hy2_port,
        // Server-side hysteria uses auth.type: userpass.
        password: `${username}:${r.hy2_pw}`,
        sni: r.hy2_host,
        obfs: "salamander",
        "obfs-password": r.hy2_obfs_pw
      });
    }
  }
  return proxies;
}

export function buildClashConfig(username: string, rows: SubscriptionRow[]): string {
  const proxies = buildProxies(username, rows);
  const names = proxies.map((p) => p.name as string);
  const out: string[] = [];

  out.push("proxies:");
  if (proxies.length === 0) {
    out[out.length - 1] = "proxies: []";
  } else {
    for (const proxy of proxies) {
      emitListItem(proxy, "  ", out);
    }
  }

  if (names.length === 0) {
    // A url-test group with no members is rejected by mihomo, so a user with no
    // nodes gets a valid config that simply routes direct.
    out.push("proxy-groups: []");
    out.push("rules:");
    out.push(`  - ${yamlString("MATCH,DIRECT")}`);
    return `${out.join("\n")}\n`;
  }

  out.push("proxy-groups:");
  emitListItem(
    {
      name: AUTO_GROUP,
      type: "url-test",
      url: "http://www.gstatic.com/generate_204",
      interval: 300,
      tolerance: 100,
      proxies: names
    },
    "  ",
    out
  );
  emitListItem(
    {
      name: SELECT_GROUP,
      type: "select",
      proxies: [AUTO_GROUP, ...names]
    },
    "  ",
    out
  );
  out.push("rules:");
  out.push(`  - ${yamlString(`MATCH,${SELECT_GROUP}`)}`);
  return `${out.join("\n")}\n`;
}
