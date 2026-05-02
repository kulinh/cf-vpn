export interface SubscriptionRow {
  vless_uuid: string;
  hy2_pw: string;
  vpn_host: string;
  hy2_host: string | null;
  hy2_port: number | null;
  hy2_obfs_pw: string | null;
  node_id: string;
  mode: string | null;
  reality_pubkey: string | null;
  reality_sid: string | null;
  reality_sni: string | null;
  xhttp_path: string | null;
}

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

export function buildHy2URI(tag: string, username: string, password: string, host: string, port: number, obfsPw: string): string {
  const enc = encodeURIComponent;
  return `hysteria2://${enc(username)}:${enc(password)}@${host}:${port}/?obfs=salamander&obfs-password=${enc(obfsPw)}&sni=${enc(host)}&insecure=0#${enc(tag)}-HY2`;
}

export function buildSubscriptionURIs(username: string, rows: SubscriptionRow[]): string {
  const lines: string[] = [];
  for (const r of rows) {
    const tag = `${username}@${r.node_id}`;
    let uri: string;
    if (r.mode === "direct" && r.reality_pubkey && r.reality_sid && r.reality_sni) {
      uri = buildVLESSRealityURI(tag, r.vless_uuid, r.vpn_host,
        r.reality_sni, r.reality_pubkey, r.reality_sid);
    } else if (r.mode === "cloudflare") {
      const path = r.xhttp_path ?? "/api/v1/sync";
      uri = buildVLESSHTTPUpgradeURI(tag, r.vless_uuid, r.vpn_host, path);
    } else {
      // Direct node missing Reality params is broken state; emit nothing
      // rather than a legacy WS+TLS URI that no current xray serves.
      continue;
    }
    lines.push(uri);
    if (r.hy2_host && r.hy2_port && r.hy2_obfs_pw) {
      lines.push(buildHy2URI(tag, username, r.hy2_pw, r.hy2_host, r.hy2_port, r.hy2_obfs_pw));
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
