export type NodeStatus = 'active' | 'degraded' | 'down' | 'unreachable' | 'disabled' | 'unknown'

export type NodeFilter = 'all' | 'active' | 'degraded' | 'down'

export type Node = {
  id: string
  label: string
  status: NodeStatus
  latencyMs: number | null
  vpnHost: string
  adminHost: string
  lastSeenAt: number | null
  zone: string
  createdAt: number
  publicIp?: string | null
  mode?: string
  hy2Host?: string | null
  hy2Port?: number | null
  hy2ObfsPw?: string | null
}

export type User = {
  id: string
  name: string
  nodes: string[]
}

export type Event = {
  id: number
  action: string
  actor: string
  outcome: string
  ts: number
}

export type UpgradeUserNodesResponse = {
  userId: string
  addedNodes: string[]
  addedCount: number
  failedCount?: number
  alreadyPresentCount: number
  totalNodesAfterUpgrade: number
}