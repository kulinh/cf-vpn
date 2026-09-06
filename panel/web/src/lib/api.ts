import { buildPublicSubscriptionUrl } from './subscriptionLinks'
import type { Event, Node, UpgradeUserNodesResponse, User } from './types'

type RotateNodeApiResponse = {
  vpn_host?: string
  hy2_host?: string | null
  hy2_port?: number | null
  public_ip?: string | null
}

type NodeApiResponse = {
  id: string
  label: string
  admin_host: string
  vpn_host: string
  zone: string
  status: string
  last_seen_at: number | null
  latency_ms: number | null
  created_at: number
  public_ip?: string | null
  mode?: string | null
  hy2_host?: string | null
  hy2_port?: number | null
  hy2_obfs_pw?: string | null
}

export type RotateNodeResponse = {
  vpnHost: string
  hy2Host?: string | null
  hy2Port?: number | null
  publicIp?: string | null
}

// Cloudflare Access redirects an expired session to a 200 OK HTML login page
// rather than a 401/302 the app can branch on via `!response.ok`. Any caller
// that expects JSON must funnel through here (or call `isSessionExpired`
// directly) so that case surfaces as a distinct, recognizable error instead
// of a raw `SyntaxError` from parsing HTML as JSON.
function isSessionExpired(response: Response): boolean {
  const contentType = response.headers.get('content-type') ?? ''
  return response.redirected || contentType.includes('text/html')
}

async function parseJsonOrThrow<T>(response: Response, label: string): Promise<T> {
  const contentType = response.headers.get('content-type') ?? ''
  if (!response.ok || !contentType.includes('application/json')) {
    if (isSessionExpired(response)) throw new Error('session-expired')
    throw new Error(`${label} failed`)
  }
  return (await response.json()) as T
}

// Throws for a non-OK response, preferring the server's {error, detail} body
// (e.g. "do NOT retry" guidance from the agent) over a generic fallback
// message. Session-expired is checked first since an expired-session
// redirect can come back as a 200 OK HTML page.
async function throwApiError(response: Response, fallback: string): Promise<never> {
  if (isSessionExpired(response)) {
    throw new Error('session-expired')
  }
  const body = (await response.json().catch(() => ({}))) as { error?: string; detail?: string }
  throw new Error(body.detail ?? body.error ?? fallback)
}

function parseRotateNodeResponse(raw: RotateNodeApiResponse): RotateNodeResponse {
  const vpnHost = raw.vpn_host

  if (vpnHost == null || vpnHost.length === 0) {
    throw new Error('rotate response missing host')
  }

  return {
    vpnHost,
    hy2Host: raw.hy2_host ?? null,
    hy2Port: raw.hy2_port ?? null,
    publicIp: raw.public_ip ?? null,
  }
}

function parseNode(raw: NodeApiResponse): Node {
  const status = raw.status as Node['status']
  return {
    id: raw.id,
    label: raw.label,
    status:
      status === 'unreachable' || status === 'degraded' || status === 'disabled'
        ? status
        : status === 'active' || status === 'down'
          ? status
          : 'unknown',
    latencyMs: raw.latency_ms ?? null,
    vpnHost: raw.vpn_host,
    adminHost: raw.admin_host,
    lastSeenAt: raw.last_seen_at ?? null,
    zone: raw.zone,
    createdAt: raw.created_at,
    publicIp: raw.public_ip ?? null,
    mode: raw.mode ?? 'direct',
    hy2Host: raw.hy2_host ?? null,
    hy2Port: raw.hy2_port ?? null,
    hy2ObfsPw: raw.hy2_obfs_pw ?? null,
  }
}

export async function rotateNode(nodeId: string): Promise<RotateNodeResponse> {
  const response = await fetch(`/api/nodes/${encodeURIComponent(nodeId)}/rotate`, {
    method: 'POST',
  })

  if (!response.ok) {
    await throwApiError(response, 'rotate failed')
  }

  const raw = await parseJsonOrThrow<RotateNodeApiResponse>(response, 'rotate')
  return parseRotateNodeResponse(raw)
}

export async function listUsers(): Promise<User[]> {
  const response = await fetch('/api/users')
  return parseJsonOrThrow<User[]>(response, 'users')
}

export async function listNodes(): Promise<Node[]> {
  const response = await fetch('/api/nodes')
  const rows = await parseJsonOrThrow<NodeApiResponse[]>(response, 'nodes')
  return rows.map(parseNode)
}

export async function listEvents(): Promise<Event[]> {
  const response = await fetch('/api/events?limit=200')
  return parseJsonOrThrow<Event[]>(response, 'events')
}

export async function upgradeUserNodes(userId: string): Promise<UpgradeUserNodesResponse> {
  const response = await fetch(`/api/users/${encodeURIComponent(userId)}/upgrade-nodes`, {
    method: 'POST',
  })
  return parseJsonOrThrow<UpgradeUserNodesResponse>(response, 'upgrade user nodes')
}

export type UserSubscription = {
  urls: string
  token: string
  subUrl: string
}

export async function getUserSubscription(userId: string): Promise<UserSubscription> {
  const response = await fetch(`/api/users/${encodeURIComponent(userId)}/subscription`)
  const data = await parseJsonOrThrow<{ subscription_url: string; sub_token: string }>(
    response,
    'subscription',
  )
  return {
    urls: data.subscription_url,
    token: data.sub_token,
    subUrl: buildPublicSubscriptionUrl(window.location.origin, data.sub_token),
  }
}

export async function healthcheckNode(nodeId: string): Promise<{ latency_ms: number }> {
  const response = await fetch(`/api/nodes/${encodeURIComponent(nodeId)}/healthcheck`, {
    method: 'POST',
  })
  return parseJsonOrThrow<{ latency_ms: number }>(response, 'healthcheck')
}

export type NodeInput = {
  id: string
  label: string
  admin_host?: string
  adminHost?: string
  vpn_host?: string
  host?: string
  zone?: string
  hy2_host?: string
}

export async function createNode(input: NodeInput): Promise<void> {
  const response = await fetch('/api/nodes', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(input),
  })

  if (isSessionExpired(response)) {
    throw new Error('session-expired')
  }

  if (!response.ok) {
    const err = (await response.json().catch(() => ({}))) as { error?: string; detail?: string }
    throw new Error(err.detail ?? err.error ?? 'create node failed')
  }
}

export async function patchNode(
  nodeId: string,
  input: Partial<NodeInput & { status: string }>,
): Promise<void> {
  const response = await fetch(`/api/nodes/${encodeURIComponent(nodeId)}`, {
    method: 'PATCH',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(input),
  })

  if (!response.ok) {
    await throwApiError(response, 'patch node failed')
  }
}

export type DeleteNodeResponse = {
  warnings: string[]
}

export async function deleteNode(nodeId: string): Promise<DeleteNodeResponse> {
  const response = await fetch(`/api/nodes/${encodeURIComponent(nodeId)}`, {
    method: 'DELETE',
  })

  if (isSessionExpired(response)) {
    throw new Error('session-expired')
  }

  if (!response.ok) {
    throw new Error('delete node failed')
  }

  const data = (await response.json().catch(() => ({}))) as { warnings?: string[] }
  return { warnings: Array.isArray(data.warnings) ? data.warnings : [] }
}

export type UserInput = {
  name: string
}

export async function createUser(input: UserInput): Promise<void> {
  const response = await fetch('/api/users', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(input),
  })

  if (isSessionExpired(response)) {
    throw new Error('session-expired')
  }

  // 207 = partial success (one or more nodes failed). Treat as success since
  // the user row exists and the panel can re-run upgrade-nodes for the
  // failed nodes. The Worker logs the partial outcome to events.
  if (response.status === 207 || response.ok) {
    return
  }
  throw new Error('create user failed')
}
