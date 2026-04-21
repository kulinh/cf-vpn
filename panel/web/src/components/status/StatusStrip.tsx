import type { NodeFilter, NodeStatus } from '../../lib/types'

type StatusCount = {
  key: NodeStatus
  label: string
  toneClass: string
  count: number
}

type StatusStripProps = {
  counts: Record<NodeStatus, number>
  avgLatencyMs: number
  filter: NodeFilter
  onFilter: (value: NodeFilter) => void
}

const statusCountsConfig: Omit<StatusCount, 'count'>[] = [
  { key: 'active', label: 'Active', toneClass: 'text-green-400' },
  { key: 'disabled', label: 'Degraded', toneClass: 'text-amber-400' },
  { key: 'unreachable', label: 'Down', toneClass: 'text-red-400' },
]

export function StatusStrip({ counts, avgLatencyMs, filter, onFilter }: StatusStripProps) {
  const statusCards: StatusCount[] = statusCountsConfig.map((status) => ({
    ...status,
    count: counts[status.key],
  }))

  return (
    <section className="grid grid-cols-2 gap-3 md:grid-cols-4">
      <button
        type="button"
        onClick={() => onFilter('all')}
        aria-pressed={filter === 'all'}
        className="rounded-lg bg-slate-900 p-3 text-left"
      >
        <p className="text-xs text-slate-400">All</p>
        <p className="text-2xl font-semibold text-slate-100">
          {statusCards.reduce((sum, status) => sum + status.count, 0)}
        </p>
      </button>

      {statusCards.map((status) => (
        <button
          key={status.key}
          type="button"
          onClick={() => onFilter(status.key)}
          aria-pressed={filter === status.key}
          className="rounded-lg bg-slate-900 p-3 text-left"
        >
          <p className="text-xs text-slate-400">{status.label}</p>
          <p className={`text-2xl font-semibold ${status.toneClass}`}>{status.count}</p>
        </button>
      ))}

      <div className="rounded-lg bg-slate-900 p-3 md:col-span-2">
        <p className="text-xs text-slate-400">Avg latency</p>
        <p className="text-2xl font-semibold text-slate-100">{avgLatencyMs} ms</p>
      </div>
    </section>
  )
}
