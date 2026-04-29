import { useEffect, useState } from 'react'
import { Toast } from '../components/ui/Toast'
import { healthcheckNode, listNodes } from '../lib/api'
import type { Node } from '../lib/types'

export function CommandCenterPage() {
  const [nodes, setNodes] = useState<Node[]>([])
  const [checkingAll, setCheckingAll] = useState(false)
  const [toastMessage, setToastMessage] = useState<string | null>(null)

  const loadNodes = async () => {
    const items = await listNodes()
    setNodes(items)
  }

  useEffect(() => {
    void loadNodes()
  }, [])

  const handleRefreshAll = async () => {
    if (nodes.length === 0) {
      setToastMessage('No nodes to check')
      return
    }
    setCheckingAll(true)
    try {
      const results = await Promise.allSettled(
        nodes.map(async (node) => {
          try {
            const { latency_ms } = await healthcheckNode(node.id)
            return { id: node.id, latencyMs: latency_ms > 0 ? latency_ms : null, status: 'active' as const }
          } catch {
            return { id: node.id, latencyMs: null, status: 'unreachable' as const }
          }
        }),
      )
      setNodes((prevNodes) =>
        prevNodes.map((node) => {
          const result = results.find((r) => r.status === 'fulfilled' && r.value.id === node.id)
          if (result?.status === 'fulfilled') {
            return { ...node, latencyMs: result.value.latencyMs, status: result.value.status }
          }
          return node
        }),
      )
      const checked = results.length
      const alive = results.filter((r) => r.status === 'fulfilled' && r.value.status === 'active').length
      setToastMessage(`Checked ${checked} nodes, ${alive} alive`)
    } catch {
      setToastMessage('Check failed')
    } finally {
      setCheckingAll(false)
    }
  }

  const getStatusBadge = (status: Node['status']) => {
    switch (status) {
      case 'active':
        return <span className="rounded bg-green-900 px-2 py-0.5 text-xs text-green-300">Active</span>
      case 'unreachable':
        return <span className="rounded bg-red-900 px-2 py-0.5 text-xs text-red-300">Unreachable</span>
      case 'down':
        return <span className="rounded bg-red-900 px-2 py-0.5 text-xs text-red-300">Down</span>
      case 'degraded':
        return <span className="rounded bg-yellow-900 px-2 py-0.5 text-xs text-yellow-300">Degraded</span>
      default:
        return <span className="rounded bg-slate-700 px-2 py-0.5 text-xs text-slate-300">Unknown</span>
    }
  }

  return (
    <>
      <section className="space-y-5">
        <div className="flex flex-wrap items-center justify-between gap-3">
          <div>
            <h1 className="text-xl font-semibold">Home</h1>
            <p className="mt-1 text-sm text-slate-400">Fleet health overview and latency snapshot.</p>
          </div>
          <button
            type="button"
            disabled={checkingAll}
            onClick={() => void handleRefreshAll()}
            className="rounded bg-indigo-600 px-3 py-1.5 text-xs text-white hover:bg-indigo-500 disabled:cursor-not-allowed disabled:opacity-60"
          >
            {checkingAll ? 'Checking...' : 'Refresh All'}
          </button>
        </div>

        <div className="overflow-hidden rounded-lg border border-slate-800">
          <table className="w-full text-sm">
            <thead className="bg-slate-900 text-slate-400">
              <tr>
                <th className="px-4 py-2 text-left">ID</th>
                <th className="px-4 py-2 text-left">Name</th>
                <th className="px-4 py-2 text-left">Mode</th>
                <th className="px-4 py-2 text-left">Latency</th>
                <th className="px-4 py-2 text-left">Status</th>
              </tr>
            </thead>
            <tbody>
              {nodes.map((node) => (
                <tr key={node.id} className="border-t border-slate-800">
                  <td className="px-4 py-2 font-mono text-xs text-slate-500">{node.id}</td>
                  <td className="px-4 py-2 font-medium text-slate-100">{node.label}</td>
                  <td className="px-4 py-2 text-slate-300">{node.mode ?? 'direct'}</td>
                  <td className="px-4 py-2 text-slate-300">{node.latencyMs == null || node.latencyMs <= 0 ? 'N/A' : `${node.latencyMs} ms`}</td>
                  <td className="px-4 py-2">{getStatusBadge(node.status)}</td>
                </tr>
              ))}
              {nodes.length === 0 && (
                <tr>
                  <td colSpan={5} className="px-4 py-8 text-center text-slate-500">
                    No nodes found
                  </td>
                </tr>
              )}
            </tbody>
          </table>
        </div>
      </section>

      <Toast message={toastMessage} onClose={() => setToastMessage(null)} />
    </>
  )
}
