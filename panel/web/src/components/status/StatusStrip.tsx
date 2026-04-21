import type { NodeFilter } from '../../lib/types'

type StatusStripProps = {
  active: number
  degraded: number
  down: number
  avgLatencyMs: number
  onFilter: (value: NodeFilter) => void
}

export function StatusStrip({ active, degraded, down, avgLatencyMs, onFilter }: StatusStripProps) {
  return (
    <section className="grid grid-cols-2 gap-3 md:grid-cols-4">
      <button
        type="button"
        onClick={() => onFilter('active')}
        className="rounded-lg bg-slate-900 p-3 text-left"
      >
        <p className="text-xs text-slate-400">Active</p>
        <p className="text-2xl font-semibold text-green-400">{active}</p>
      </button>

      <button
        type="button"
        onClick={() => onFilter('degraded')}
        className="rounded-lg bg-slate-900 p-3 text-left"
      >
        <p className="text-xs text-slate-400">Degraded</p>
        <p className="text-2xl font-semibold text-amber-400">{degraded}</p>
      </button>

      <button
        type="button"
        onClick={() => onFilter('down')}
        className="rounded-lg bg-slate-900 p-3 text-left"
      >
        <p className="text-xs text-slate-400">Down</p>
        <p className="text-2xl font-semibold text-red-400">{down}</p>
      </button>

      <div className="rounded-lg bg-slate-900 p-3">
        <p className="text-xs text-slate-400">Avg latency</p>
        <p className="text-2xl font-semibold text-slate-100">{avgLatencyMs} ms</p>
      </div>
    </section>
  )
}
