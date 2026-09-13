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
  // The bot's own @username (without @). Commands addressed to another bot in
  // the group are ignored, and "unknown command" is only answered when the
  // message named this bot explicitly.
  TELEGRAM_BOT_USERNAME?: string;
  // Public origin of the Access-fronted custom domain. The Telegram webhook is
  // served on *.workers.dev, where /sub/* is 404 by design, so subscription
  // links must be built from this rather than from the request origin.
  PANEL_PUBLIC_ORIGIN?: string;
  // Override for where the sr_proxy_list_<CN|UAE>.module files inlined into
  // the Shadowrocket .conf are fetched from (default: the raw GitHub URL of
  // kulinh/shadowrocket-vietnamese, see lib/cnrules.ts).
  RULES_BASE_URL?: string;
  // Username/password fallback used when Cloudflare Access is not in front of
  // the Worker. Both must be set for it to apply; see lib/auth.ts.
  PANEL_BASIC_USER?: string;
  PANEL_BASIC_PASS?: string;
  // Optional Cloudflare Access JWKS verification gate (see lib/auth.ts NOTE).
  // When both are set (and a JWKS-capable JWT library is available), the Worker
  // should verify the CF-Access-Jwt-Assertion signature instead of only
  // checking header presence.
  ACCESS_TEAM_DOMAIN?: string;
  ACCESS_AUD?: string;
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
  // Always present on /status (no omitempty on the Go side); the direct-route
  // pair is omitted when unset.
  xhttp_enabled?: boolean;
  xhttp_direct_host?: string;
  xhttp_direct_path?: string;
  // Direct-mode only; omitted when the node has no H3 route.
  xhttp_h3_host?: string;
  xhttp_h3_path?: string;
  // NaiveProxy route, any mode; omitted when the node has none.
  naive_host?: string;
  naive_user?: string;
  naive_pass?: string;
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
  // Not emitted by /sync today (only /status carries them); typed so the same
  // merge applies once the agent adds them.
  xhttp_enabled?: boolean;
  xhttp_direct_host?: string;
  xhttp_direct_path?: string;
  xhttp_h3_host?: string;
  xhttp_h3_path?: string;
  naive_host?: string;
  naive_user?: string;
  naive_pass?: string;
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
  xhttp_enabled: number;
  xhttp_direct_host: string | null;
  xhttp_direct_path: string | null;
  xhttp_h3_host: string | null;
  xhttp_h3_path: string | null;
  naive_host: string | null;
  naive_user: string | null;
  naive_pass: string | null;
  agent_secret: string | null;
  tunnel_uuid: string | null;
}

export interface UserRow {
  id: string;
  name: string;
  created_at: number;
}
