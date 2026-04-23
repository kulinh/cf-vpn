import { useEffect, useState } from 'react'
import { ConfirmDialog } from '../components/ui/ConfirmDialog'
import { Toast } from '../components/ui/Toast'
import { healthcheckNode, listNodes, rotateNode } from '../lib/api'
import type { Node } from '../lib/types'

export function CommandCenterPage() {
  const [nodes, setNodes] = useState<Node[]>([])
  const [confirmNodeId, setConfirmNodeId] = useState<string | null>(null)
  const [rotatingNodeId, setRotatingNodeId] = useState<string | null>(null)
  const [checkingAll, setCheckingAll] = useState(false)
  const [toastMessage, setToastMessage] = useState<string | null>(null)

  const loadNodes = async () => {
    const items = await listNodes()
    setNodes(items)
  }

  useEffect(() => {
    void loadNodes()
  }, [])

  const confirmingNode = nodes.find((node) => node.id === confirmNodeId) ?? null

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
            return { id: node.id, latencyMs: latency_ms, status: 'active' as const }
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

  const handleConfirmRotate = async () => {
    if (confirmNodeId == null) return
    const nodeId = confirmNodeId
    setConfirmNodeId(null)
    setRotatingNodeId(nodeId)

    try {
      const { vpnHost } = await rotateNode(nodeId)
      setNodes((prevNodes) =>
        prevNodes.map((node) => (node.id === nodeId ? { ...node, vpnHost } : node)),
      )
      setToastMessage('Rotated successfully')
    } catch {
      setToastMessage('Rotate failed')
    } finally {
      setRotatingNodeId(null)
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
      <section className="space-y-3">
        <div className="flex items-center justify-between">
          <h1 className="text-xl font-semibold">Home</h1>
          <button
            type="button"
            disabled={checkingAll}
            onClick={() => void handleRefreshAll()}
            className="rounded bg-indigo-600 px-3 py-1 text-xs text-white hover:bg-indigo-500 disabled:cursor-not-allowed disabled:opacity-60"
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
                <th className="px-4 py-2 text-left">VPN Host</th>
                <th className="px-4 py-2 text-left">Status</th>
                <th className="px-4 py-2"></th>
              </tr>
            </thead>
            <tbody>
              {nodes.map((node) => (
                <tr key={node.id} className="border-t border-slate-800">
                  <td className="px-4 py-2 font-mono text-xs text-slate-400">{node.id}</td>
                  <td className="px-4 py-2 text-slate-100">{node.label}</td>
                  <td className="px-4 py-2 font-mono text-xs text-slate-400">{node.vpnHost}</td>
                  <td className="px-4 py-2">{getStatusBadge(node.status)}</td>
                  <td className="px-4 py-2">
                    <button
                      type="button"
                      disabled={rotatingNodeId != null}
                      onClick={() => setConfirmNodeId(node.id)}
                      className="rounded bg-indigo-500 px-3 py-1 text-xs text-white disabled:cursor-not-allowed disabled:opacity-50"
                    >
                      {rotatingNodeId === node.id ? 'Rotating...' : 'Rotate'}
                    </button>
                  </td>
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

      <ConfirmDialog
        open={confirmNodeId != null}
        title={`Rotate ${confirmingNode?.label ?? 'node'} host?`}
        message="This requests a fresh VPN host assignment for the selected node."
        confirmLabel="Confirm rotate"
        onConfirm={() => {
          void handleConfirmRotate()
        }}
        onCancel={() => setConfirmNodeId(null)}
      />

      <Toast message={toastMessage} onClose={() => setToastMessage(null)} />
    </>
  )
}
