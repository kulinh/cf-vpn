import { buildPublicSubscriptionUrl } from './subscriptionLinks'
import type { Event, Node, UpgradeUserNodesResponse, User } from './types'

type RotateNodeApiResponse = {
  new_host?: string
  vpn_host?: string
  tunnel_uuid?: string
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
  tunnelUuid?: string
  hy2Host?: string | null
  hy2Port?: number | null
  publicIp?: string | null
}

function parseRotateNodeResponse(raw: RotateNodeApiResponse): RotateNodeResponse {
  const vpnHost = raw.new_host ?? raw.vpn_host

  if (vpnHost == null || vpnHost.length === 0) {
    throw new Error('rotate response missing host')
  }

  return {
    vpnHost,
    tunnelUuid: raw.tunnel_uuid,
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
    status: status === 'unreachable' || status === 'degraded' ? status : status === 'active' || status === 'down' ? status : 'unknown',
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
    throw new Error('rotate failed')
  }

  return parseRotateNodeResponse((await response.json()) as RotateNodeApiResponse)
}

export async function listUsers(): Promise<User[]> {
  const response = await fetch('/api/users')

  if (!response.ok) {
    throw new Error('users failed')
  }

  return (await response.json()) as User[]
}

export async function listNodes(): Promise<Node[]> {
  const response = await fetch('/api/nodes')

  if (!response.ok) {
    throw new Error('nodes failed')
  }

  const rows = (await response.json()) as NodeApiResponse[]
  return rows.map(parseNode)
}

export async function listEvents(): Promise<Event[]> {
  const response = await fetch('/api/events?limit=200')

  if (!response.ok) {
    throw new Error('events failed')
  }

  return (await response.json()) as Event[]
}

export async function upgradeUserNodes(userId: string): Promise<UpgradeUserNodesResponse> {
  const response = await fetch(`/api/users/${encodeURIComponent(userId)}/upgrade-nodes`, {
    method: 'POST',
  })

  if (!response.ok) {
    throw new Error('upgrade user nodes failed')
  }

  return (await response.json()) as UpgradeUserNodesResponse
}

export type UserSubscription = {
  urls: string
  token: string
  subUrl: string
}

export async function getUserSubscription(userId: string): Promise<UserSubscription> {
  const response = await fetch(`/api/users/${encodeURIComponent(userId)}/subscription`)

  if (!response.ok) {
    throw new Error('subscription failed')
  }

  const data = (await response.json()) as { subscription_url: string; sub_token: string }
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

  if (!response.ok) {
    const err = await response.text()
    throw new Error(`healthcheck failed: ${response.status} ${err}`)
  }

  return (await response.json()) as { latency_ms: number }
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
    throw new Error('patch node failed')
  }
}

export type DeleteNodeResponse = {
  warnings: string[]
}

export async function deleteNode(nodeId: string): Promise<DeleteNodeResponse> {
  const response = await fetch(`/api/nodes/${encodeURIComponent(nodeId)}`, {
    method: 'DELETE',
  })

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

  if (!response.ok) {
    throw new Error('create user failed')
  }
}