import type { NodeFilter } from '../../lib/types'

type StatusFilter = Exclude<NodeFilter, 'all'>

type StatusStripProps = {
  active: number
  degraded: number
  down: number
  avgLatencyMs: number
  filter: NodeFilter
  onFilter: (value: NodeFilter) => void
}

const statusCardsConfig: Array<{ key: StatusFilter; label: string; toneClass: string }> = [
  { key: 'active', label: 'Active', toneClass: 'text-green-400' },
  { key: 'degraded', label: 'Degraded', toneClass: 'text-amber-400' },
  { key: 'down', label: 'Down', toneClass: 'text-red-400' },
]

export function StatusStrip({ active, degraded, down, avgLatencyMs, filter, onFilter }: StatusStripProps) {
  const counts: Record<StatusFilter, number> = { active, degraded, down }

  return (
    <section className="grid grid-cols-2 gap-3 md:grid-cols-4">
      {statusCardsConfig.map((status) => (
        <button
          key={status.key}
          type="button"
          onClick={() => onFilter(filter === status.key ? 'all' : status.key)}
          aria-pressed={filter === status.key}
          className="rounded-lg bg-slate-900 p-3 text-left"
        >
          <p className="text-xs text-slate-400">{status.label}</p>
          <p className={`text-2xl font-semibold ${status.toneClass}`}>{counts[status.key]}</p>
        </button>
      ))}

      <div className="rounded-lg bg-slate-900 p-3 text-left">
        <p className="text-xs text-slate-400">Avg latency</p>
        <p className="text-2xl font-semibold text-slate-100">{avgLatencyMs} ms</p>
      </div>
    </section>
  )
}
