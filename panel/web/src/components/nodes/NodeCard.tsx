import type { Node } from '../../lib/types'

type NodeCardProps = {
  node: Node
  onRotate: (id: string) => void
  onHealthcheck: (id: string) => void
  onOpen: (id: string) => void
}

export function NodeCard({ node, onRotate, onHealthcheck, onOpen }: NodeCardProps) {
  return (
    <article className="space-y-3 rounded-xl border border-slate-800 bg-slate-900 p-4">
      <div className="flex items-center justify-between">
        <h3 className="font-semibold text-slate-100">{node.label}</h3>
        <span className="text-xs uppercase tracking-wide text-slate-300">{node.status}</span>
      </div>

      <p className="text-sm text-slate-300">{node.latencyMs == null ? 'N/A' : `${node.latencyMs} ms`}</p>
      <p className="break-all text-xs text-slate-400">{node.vpnHost}</p>

      <div className="flex flex-wrap gap-2">
        <button
          type="button"
          onClick={() => onRotate(node.id)}
          className="rounded bg-indigo-500 px-3 py-1 text-white"
        >
          Rotate
        </button>
        <button
          type="button"
          onClick={() => onHealthcheck(node.id)}
          className="rounded bg-slate-800 px-3 py-1 text-slate-100"
        >
          Healthcheck
        </button>
        <button
          type="button"
          onClick={() => onOpen(node.id)}
          className="rounded bg-slate-800 px-3 py-1 text-slate-100"
        >
          Open
        </button>
      </div>
    </article>
  )
}
