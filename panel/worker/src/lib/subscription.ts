export interface SubscriptionRow {
  vless_uuid: string;
  hy2_pw: string;
  vpn_host: string;
  // Public IPv4 of a direct node. Reality URIs address the node by IP so the
  // client never resolves vpn_host (DNS for it is interfered with from China).
  // Optional so older callers/tests that predate the column still type-check.
  public_ip?: string | null;
  hy2_host: string | null;
  hy2_port: number | null;
  hy2_obfs_pw: string | null;
  node_id: string;
  mode: string | null;
  reality_pubkey: string | null;
  reality_sid: string | null;
  reality_sni: string | null;
  xhttp_path: string | null;
  // 1 when the node also serves the XHTTP inbound (cfvpnctl xhttp enable).
  // Optional so rows that predate the column still type-check.
  xhttp_enabled?: number | null;
  // Direct XHTTP route (TLS front on the node's own hostname); both set = on.
  xhttp_direct_host?: string | null;
  xhttp_direct_path?: string | null;
  // XHTTP-over-H3 route on a DIRECT node: xray serves it on UDP 443 under a
  // real certificate, next to REALITY on TCP 443. Both set = on.
  xhttp_h3_host?: string | null;
  xhttp_h3_path?: string | null;
}

export const XHTTP_DIRECT_MODE = "stream-one";

// Nothing sits in front of the node on this route, so a single bidirectional
// stream is fine. Mirrors templates.XHTTPH3Mode in Go.
export const XHTTP_H3_MODE = "stream-one";

export const XHTTP_PATH = "/api/v2/stream";
export const XHTTP_MODE = "packet-up";

// Server-side hysteria uses `auth.type: userpass`, so the URI must include the
// username before the password. Without it, the client gets a 404 auth error.
export function buildVLESSRealityURI(
  name: string, uuid: string, host: string,
  sni: string, pbk: string, sid: string,
): string {
  const enc = encodeURIComponent;
  return `vless://${uuid}@${host}:443?encryption=none&security=reality&flow=xtls-rprx-vision&type=tcp&sni=${enc(sni)}&pbk=${enc(pbk)}&sid=${enc(sid)}&fp=chrome#${enc(name)}-Reality`;
}

export function buildVLESSHTTPUpgradeURI(
  name: string, uuid: string, domain: string, path: string,
): string {
  const enc = encodeURIComponent;
  const encPath = path.split("/").map(enc).join("%2F");
  return `vless://${uuid}@${domain}:443?encryption=none&security=tls&type=httpupgrade&host=${enc(domain)}&path=${encPath}&sni=${enc(domain)}#${enc(name)}-HTTPUpgrade`;
}

// address is what the client dials (public IP when known), sniHost the
// hostname the HY2 certificate was issued for. Mirrors BuildHy2URI in Go.
// Mirrors BuildVLESSXHTTPURI in internal/subscription.
export function buildVLESSXHTTPURI(name: string, uuid: string, domain: string, path: string, mode: string): string {
  const enc = encodeURIComponent;
  const encPath = path.split("/").map(enc).join("%2F");
  return `vless://${uuid}@${domain}:443?encryption=none&security=tls&type=xhttp&host=${enc(domain)}&path=${encPath}&mode=${enc(mode)}&sni=${enc(domain)}#${enc(name)}-XHTTP`;
}

export function hasXHTTP(r: SubscriptionRow): boolean {
  return isCloudflareRow(r) && !!r.xhttp_enabled;
}

export function hasXHTTPDirect(r: SubscriptionRow): boolean {
  return isCloudflareRow(r) && !!r.xhttp_direct_host && !!r.xhttp_direct_path;
}

// The H3 inbound only exists on direct-mode nodes. It is deliberately NOT
// gated on isRealityRow: it is a separate inbound and stays serviceable even
// if the row's Reality params are incomplete.
export function hasXHTTPH3(r: SubscriptionRow): boolean {
  return r.mode === "direct" && !!r.xhttp_h3_host && !!r.xhttp_h3_path;
}

// Mirrors BuildVLESSXHTTPDirectURI in internal/subscription: real TLS on the
// node's own hostname, so the address is the hostname, never the IP.
export function buildVLESSXHTTPDirectURI(name: string, uuid: string, host: string, path: string, mode: string): string {
  const enc = encodeURIComponent;
  const encPath = path.split("/").map(enc).join("%2F");
  return `vless://${uuid}@${host}:443?encryption=none&security=tls&type=xhttp&host=${enc(host)}&path=${encPath}&mode=${enc(mode)}&sni=${enc(host)}#${enc(name)}-XHTTP-Direct`;
}

// Mirrors BuildVLESSXHTTPH3URI in internal/subscription. The address is the
// hostname the certificate was issued for, never the public IP: unlike
// REALITY this route presents a real certificate and the SNI must match it.
export function buildVLESSXHTTPH3URI(name: string, uuid: string, host: string, path: string, mode: string): string {
  const enc = encodeURIComponent;
  const encPath = path.split("/").map(enc).join("%2F");
  return `vless://${uuid}@${host}:443?encryption=none&security=tls&type=xhttp&host=${enc(host)}&path=${encPath}&mode=${enc(mode)}&alpn=h3&sni=${enc(host)}#${enc(name)}-XHTTP-H3`;
}

export function buildHy2URI(tag: string, username: string, password: string, address: string, sniHost: string, port: number, obfsPw: string): string {
  const enc = encodeURIComponent;
  return `hysteria2://${enc(username)}:${enc(password)}@${address}:${port}/?obfs=salamander&obfs-password=${enc(obfsPw)}&sni=${enc(sniHost)}&insecure=0#${enc(tag)}-HY2`;
}

// hy2Address is what an HY2 client dials: the node's public IP when D1 has
// one, else the HY2 hostname. Same fallback rule as realityHost.
export function hy2Address(r: SubscriptionRow): string {
  return r.public_ip && r.public_ip.length > 0 ? r.public_ip : r.hy2_host!;
}

// Row predicates shared by the base64, Clash and Shadowrocket builders so the
// three formats never disagree about which nodes a user has.
export function isRealityRow(r: SubscriptionRow): boolean {
  return r.mode === "direct" && !!r.reality_pubkey && !!r.reality_sid && !!r.reality_sni;
}
export function isCloudflareRow(r: SubscriptionRow): boolean {
  return r.mode === "cloudflare";
}
export function hasHy2(r: SubscriptionRow): boolean {
  return !!r.hy2_host && !!r.hy2_port && !!r.hy2_obfs_pw;
}

// realityHost is what a Reality client dials: the node's public IP when D1
// has one, else the hostname (legacy rows, or a node whose agent has not yet
// reported an IP).
export function realityHost(r: SubscriptionRow): string {
  return r.public_ip && r.public_ip.length > 0 ? r.public_ip : r.vpn_host;
}

const warnedMissingObfs = new Set<string>();

function warnMissingObfs(nodeId: string): void {
  if (warnedMissingObfs.has(nodeId)) {
    return;
  }
  warnedMissingObfs.add(nodeId);
  console.warn("hy2 line dropped: node has hy2_host/hy2_port but no hy2_obfs_pw:", nodeId);
}

export function buildSubscriptionURIs(username: string, rows: SubscriptionRow[]): string {
  const lines: string[] = [];
  for (const r of rows) {
    const tag = `${username}@${r.node_id}`;
    let uri: string;
    if (isRealityRow(r)) {
      uri = buildVLESSRealityURI(tag, r.vless_uuid, realityHost(r),
        r.reality_sni!, r.reality_pubkey!, r.reality_sid!);
    } else if (isCloudflareRow(r)) {
      const path = r.xhttp_path ?? "/api/v1/sync";
      uri = buildVLESSHTTPUpgradeURI(tag, r.vless_uuid, r.vpn_host, path);
    } else {
      // Direct node missing Reality params is broken state; emit nothing
      // rather than a legacy WS+TLS URI that no current xray serves.
      continue;
    }
    lines.push(uri);
    if (hasXHTTP(r)) {
      lines.push(buildVLESSXHTTPURI(tag, r.vless_uuid, r.vpn_host, XHTTP_PATH, XHTTP_MODE));
    }
    if (hasXHTTPDirect(r)) {
      lines.push(buildVLESSXHTTPDirectURI(tag, r.vless_uuid, r.xhttp_direct_host!, r.xhttp_direct_path!, XHTTP_DIRECT_MODE));
    }
    if (hasXHTTPH3(r)) {
      lines.push(buildVLESSXHTTPH3URI(tag, r.vless_uuid, r.xhttp_h3_host!, r.xhttp_h3_path!, XHTTP_H3_MODE));
    }
    if (hasHy2(r)) {
      lines.push(buildHy2URI(tag, username, r.hy2_pw, hy2Address(r), r.hy2_host!, r.hy2_port!, r.hy2_obfs_pw!));
    } else if (r.hy2_host && r.hy2_port) {
      // The node has a Hysteria2 endpoint but no obfs password, so the line is
      // dropped and the user silently loses HY2 on that node. Output is
      // unchanged — this only makes the drop visible in the logs, once per node
      // per isolate.
      warnMissingObfs(r.node_id);
    }
  }
  return lines.join("\n");
}

export function buildSubscriptionForClient(
  username: string,
  rows: SubscriptionRow[]
): { subscription_url: string } {
  return { subscription_url: buildSubscriptionURIs(username, rows) };
}

export function encodeSubscriptionBody(uris: string | string[], remarks?: string): string {
  const body = Array.isArray(uris) ? uris.join("\n") : uris;
  const plain = remarks != null && remarks.length > 0
    ? body.length > 0 ? `REMARKS=${remarks}\n${body}` : `REMARKS=${remarks}`
    : body;
  const bytes = new TextEncoder().encode(plain);
  let binary = "";
  for (const b of bytes) binary += String.fromCharCode(b);
  return btoa(binary);
}
