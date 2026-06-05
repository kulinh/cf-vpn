export interface Env {
  DB: D1Database;
  CF_ACCESS_CLIENT_ID?: string;
  CF_ACCESS_CLIENT_SECRET?: string;
  SERVICE_TOKEN_HEADER_ID?: string;
  SERVICE_TOKEN_HEADER_SECRET?: string;
  ADMIN_HOST_ALLOWED_SUFFIXES?: string;
  CF_API_TOKEN?: string;
  CF_ACCOUNT_ID?: string;
  AGENT_SHARED_SECRET?: string;
  TELEGRAM_BOT_TOKEN?: string;
  TELEGRAM_WEBHOOK_SECRET?: string;
  TELEGRAM_GROUP_ID?: string;
}

export interface ApiError {
  error: string;
  detail?: string;
  [key: string]: unknown;
}

export interface AgentError {
  error: string;
  detail?: string;
}

export interface AgentUserRecord {
  name: string;
  vless_uuid: string;
  hy2_pw: string;
}

export interface AgentAddUserResponse {
  name: string;
  vless_uuid: string;
  hy2_pw: string;
}

export interface AgentStatusResponse {
  xray: string;
  cloudflared: string;
  hysteria: string;
  vpn_host: string;
  zone?: string;
  public_ip?: string;
  mode?: string;
  hy2_host?: string;
  hy2_port?: number;
  hy2_obfs_pw?: string;
  tunnel_uuid: string;
  last_rotate_at: number;
  reality_pubkey?: string;
  reality_sid?: string;
  reality_sni?: string;
  reality_dest?: string;
  xhttp_path?: string;
}

export interface AgentHealthcheckResponse {
  ok: boolean;
  code: number;
  latency_ms: number;
}

export interface AgentRotateResponse {
  vpn_host: string;
  public_ip: string;
  hy2_host: string;
  hy2_port: number;
  hy2_obfs_pw: string;
}

export interface AgentSyncResponse {
  ok: boolean;
  vpn_host: string;
  public_ip: string;
  hy2_host: string;
  hy2_port?: number;
  hy2_obfs_pw?: string;
  users: number;
  mode?: string;
  reality_pubkey?: string;
  reality_sid?: string;
  reality_sni?: string;
  reality_dest?: string;
  xhttp_path?: string;
}

export interface NodeRow {
  id: string;
  label: string;
  admin_host: string;
  vpn_host: string;
  zone: string;
  status: string;
  last_seen_at: number | null;
  latency_ms: number | null;
  created_at: number;
  public_ip: string | null;
  mode: string;
  hy2_host: string | null;
  hy2_port: number | null;
  hy2_obfs_pw: string | null;
  reality_pubkey: string | null;
  reality_sid: string | null;
  reality_sni: string | null;
  reality_dest: string | null;
  xhttp_path: string | null;
  agent_secret: string | null;
  tunnel_uuid: string | null;
}

export interface UserRow {
  id: string;
  name: string;
  created_at: number;
}
