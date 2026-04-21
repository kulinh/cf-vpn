import type { Event, User } from './types'

type RotateNodeApiResponse = {
  new_host?: string
  vpn_host?: string
  tunnel_uuid?: string
}

export type RotateNodeResponse = {
  vpnHost: string
  tunnelUuid?: string
}

function parseRotateNodeResponse(raw: RotateNodeApiResponse): RotateNodeResponse {
  const vpnHost = raw.new_host ?? raw.vpn_host

  if (vpnHost == null || vpnHost.length === 0) {
    throw new Error('rotate response missing host')
  }

  return {
    vpnHost,
    tunnelUuid: raw.tunnel_uuid,
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

export async function listEvents(): Promise<Event[]> {
  const response = await fetch('/api/events?limit=200')

  if (!response.ok) {
    throw new Error('events failed')
  }

  return (await response.json()) as Event[]
}
