import type { Node } from '../../lib/types'

type NodeCardProps = {
  node: Node
  onRotate: (id: string) => void
  onHealthcheck: (id: string) => void
  onOpen: (id: string) => void
  rotateDisabled?: boolean
  rotating?: boolean
}

export function NodeCard({
  node,
  onRotate,
  onHealthcheck,
  onOpen,
  rotateDisabled = false,
  rotating = false,
}: NodeCardProps) {
  return (
    <article className="space-y-3 rounded-xl border border-slate-800 bg-slate-900 p-4">
      <div className="flex items-center justify-between">
        <h3 className="font-semibold text-slate-100">{node.label}</h3>
        <span className="text-xs uppercase tracking-wide text-slate-300">{node.status}</span>
      </div>

      <p className="text-sm text-slate-300">{node.latencyMs == null ? 'N/A' : `${node.latencyMs} ms`}</p>
      <dl className="space-y-1 text-xs text-slate-400">
        <div>
          <dt className="inline text-slate-500">VLESS </dt>
          <dd className="inline break-all font-mono">{node.vpnHost}</dd>
        </div>
        {node.hy2Host && (
          <div>
            <dt className="inline text-slate-500">HY2 </dt>
            <dd className="inline break-all font-mono">{node.hy2Host}:{node.hy2Port ?? 'N/A'}</dd>
          </div>
        )}
        {node.publicIp && (
          <div>
            <dt className="inline text-slate-500">IP </dt>
            <dd className="inline font-mono">{node.publicIp}</dd>
          </div>
        )}
        <div>
          <dt className="inline text-slate-500">Mode </dt>
          <dd className="inline font-mono">{node.mode}</dd>
        </div>
      </dl>

      <div className="flex flex-wrap gap-2">
        <button
          type="button"
          disabled={rotateDisabled}
          onClick={() => onRotate(node.id)}
          className="rounded bg-indigo-500 px-3 py-1 text-white disabled:cursor-not-allowed disabled:opacity-50"
        >
          {rotating ? 'Rotating...' : 'Rotate'}
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
