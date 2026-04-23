export type NodeStatus = 'active' | 'degraded' | 'down' | 'unreachable' | 'unknown'

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
  alreadyPresentCount: number
  totalNodesAfterUpgrade: number
}