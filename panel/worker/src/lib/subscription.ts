export interface SubscriptionRow {
  vless_uuid: string;
  hy2_pw: string;
  vpn_host: string;
  node_id: string;
  hy2_host: string | null;
  hy2_port: number | null;
  hy2_obfs_pw: string | null;
}

function buildVLESSURI(name: string, uuid: string, domain: string): string {
  return `vless://${uuid}@${domain}:443?encryption=none&security=tls&type=ws&host=${domain}&path=%2Fvless&sni=${domain}#${name}-VLESS`;
}

function buildHY2URI(name: string, username: string, password: string, host: string, port: number, obfsPW: string | null): string {
  const params = new URLSearchParams({ insecure: "1", obfs: "salamander", "obfs-password": obfsPW ?? "", pinSHA256: "auto" });
  return `hysteria2://${encodeURIComponent(username)}:${encodeURIComponent(password)}@${host}:${port}?${params.toString()}#${name}-HY2`;
}

export function buildSubscriptionURIs(username: string, rows: SubscriptionRow[]): string[] {
  const lines: string[] = [];
  for (const row of rows) {
    lines.push(buildVLESSURI(`${username}-${row.node_id}`, row.vless_uuid, row.vpn_host));
    if (row.hy2_host && row.hy2_port) {
      lines.push(buildHY2URI(`${username}-${row.node_id}`, username, row.hy2_pw, row.hy2_host, row.hy2_port, row.hy2_obfs_pw));
    }
  }
  return lines;
}

export function buildSubscriptionForClient(
  username: string,
  rows: SubscriptionRow[]
): { subscription_url: string } {
  return { subscription_url: buildSubscriptionURIs(username, rows).join("\n") };
}

export function encodeSubscriptionBody(uris: string[], remarks?: string): string {
  const lines = remarks != null && remarks.length > 0 ? [`REMARKS=${remarks}`, ...uris] : uris;
  const plain = lines.join("\n");

  let binary = "";
  const bytes = new TextEncoder().encode(plain);
  const chunkSize = 0x8000;
  for (let i = 0; i < bytes.length; i += chunkSize) {
    binary += String.fromCharCode(...bytes.subarray(i, i + chunkSize));
  }
  return btoa(binary);
}
