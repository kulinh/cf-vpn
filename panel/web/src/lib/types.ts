export type NodeStatus = 'active' | 'degraded' | 'down' | 'unknown'

export type NodeFilter = 'all' | 'active' | 'degraded' | 'down'

export type Node = {
  id: string
  label: string
  status: NodeStatus
  latencyMs: number | null
  vpnHost: string
  lastSeenAt: number | null
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
