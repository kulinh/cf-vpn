import { useMemo, useState } from 'react'
import { StatusStrip } from '../components/status/StatusStrip'
import type { Node, NodeFilter, NodeStatus } from '../lib/types'

const demoNodes: Node[] = [
  {
    id: 'sg',
    label: 'Singapore',
    status: 'active',
    latencyMs: 82,
    vpnHost: 'sg.example.com',
    lastSeenAt: Date.now(),
  },
  {
    id: 'jp',
    label: 'Tokyo',
    status: 'disabled',
    latencyMs: 182,
    vpnHost: 'jp.example.com',
    lastSeenAt: Date.now(),
  },
  {
    id: 'hk',
    label: 'Hong Kong',
    status: 'unreachable',
    latencyMs: null,
    vpnHost: 'hk.example.com',
    lastSeenAt: null,
  },
]

const statusKeys: NodeStatus[] = ['active', 'disabled', 'unreachable']

export function CommandCenterPage() {
  const [filter, setFilter] = useState<NodeFilter>('all')

  const counts = useMemo(
    () =>
      statusKeys.reduce(
        (acc, status) => ({
          ...acc,
          [status]: demoNodes.filter((node) => node.status === status).length,
        }),
        { active: 0, disabled: 0, unreachable: 0 },
      ),
    [],
  )

  const avgLatency = useMemo(() => {
    const latencies = demoNodes
      .map((node) => node.latencyMs)
      .filter((latency): latency is number => latency != null)

    if (latencies.length === 0) {
      return 0
    }

    return Math.round(latencies.reduce((sum, latency) => sum + latency, 0) / latencies.length)
  }, [])

  const filteredNodes =
    filter === 'all' ? demoNodes : demoNodes.filter((node) => node.status === filter)

  return (
    <div className="space-y-4">
      <StatusStrip counts={counts} avgLatencyMs={avgLatency} filter={filter} onFilter={setFilter} />

      <section className="rounded-lg bg-slate-900 p-3 text-sm text-slate-300">
        Showing {filteredNodes.length} node{filteredNodes.length === 1 ? '' : 's'}
      </section>
    </div>
  )
}
