import type { Env } from "../types";
import { all } from "./db";

export interface SubscriptionRow {
  vless_uuid: string;
  hy2_pw: string;
  vpn_host: string;
  hy2_host: string | null;
  hy2_port: number | null;
  hy2_obfs_pw: string | null;
  node_id: string;
}

export function buildVLESSURI(name: string, uuid: string, domain: string): string {
  const enc = encodeURIComponent;
  return `vless://${uuid}@${domain}:443?encryption=none&security=tls&type=ws&host=${enc(domain)}&path=%2Fvless&sni=${enc(domain)}#${enc(name)}-VLESS`;
}

// Server-side hysteria uses `auth.type: userpass`, so the URI must include the
// username before the password. Without it, the client gets a 404 auth error.
export function buildHy2URI(tag: string, username: string, password: string, host: string, port: number, obfsPw: string): string {
  const enc = encodeURIComponent;
  return `hysteria2://${enc(username)}:${enc(password)}@${host}:${port}/?obfs=salamander&obfs-password=${enc(obfsPw)}&sni=${enc(host)}&insecure=0#${enc(tag)}-HY2`;
}

export function buildSubscriptionURIs(username: string, rows: SubscriptionRow[]): string {
  const lines: string[] = [];
  for (const r of rows) {
    const tag = `${username}@${r.node_id}`;
    lines.push(buildVLESSURI(tag, r.vless_uuid, r.vpn_host));
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

export function loadSubscriptionRows(env: Env, userId: string): Promise<SubscriptionRow[]> {
  return all<SubscriptionRow>(
    env.DB.prepare(
      "SELECT un.vless_uuid, un.hy2_pw, n.vpn_host, n.hy2_host, n.hy2_port, n.hy2_obfs_pw, un.node_id FROM user_nodes un JOIN nodes n ON n.id=un.node_id WHERE un.user_id=? ORDER BY un.node_id"
    ).bind(userId)
  );
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
