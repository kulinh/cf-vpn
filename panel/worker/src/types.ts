export interface Env {
  DB: D1Database;
  CF_ACCESS_CLIENT_ID?: string;
  CF_ACCESS_CLIENT_SECRET?: string;
  SERVICE_TOKEN_HEADER_ID?: string;
  SERVICE_TOKEN_HEADER_SECRET?: string;
  ADMIN_HOST_ALLOWED_SUFFIXES?: string;
}

export interface ApiError {
  error: string;
  detail?: string;
}

export interface AgentError {
  error: string;
  detail?: string;
}

export interface AgentUserRecord {
  name: string;
  vless_uuid: string;
  trojan_pw: string;
}

export interface AgentAddUserResponse {
  name: string;
  vless_uuid: string;
  trojan_pw: string;
}

export interface AgentStatusResponse {
  xray: string;
  cloudflared: string;
  vpn_host: string;
  tunnel_uuid: string;
  last_rotate_at: number;
}

export interface AgentHealthcheckResponse {
  ok: boolean;
  code: number;
  latency_ms: number;
}

export interface AgentRotateResponse {
  tunnel_uuid: string;
  vpn_host: string;
}

export interface AgentSyncResponse {
  added: string[];
  removed: string[];
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
}

export interface UserRow {
  id: string;
  name: string;
  created_at: number;
}
