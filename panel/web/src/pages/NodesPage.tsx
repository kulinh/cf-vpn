import { useEffect, useState } from 'react'
import { ConfirmDialog } from '../components/ui/ConfirmDialog'
import { ErrorBanner } from '../components/ui/ErrorBanner'
import { Toast } from '../components/ui/Toast'
import { deleteNode, healthcheckNode, listNodes, patchNode, rotateNode } from '../lib/api'
import { describeLoadError } from '../lib/errors'
import type { Node } from '../lib/types'
import type { NodeInput } from '../lib/api'

export function NodesPage() {
  const [nodes, setNodes] = useState<Node[]>([])
  const [confirmNodeId, setConfirmNodeId] = useState<string | null>(null)
  const [rotatingNodeId, setRotatingNodeId] = useState<string | null>(null)
  const [checkingNodeId, setCheckingNodeId] = useState<string | null>(null)
  const [checkingAll, setCheckingAll] = useState(false)
  const [editingNode, setEditingNode] = useState<Node | null>(null)
  const [editValues, setEditValues] = useState<Partial<NodeInput>>({})
  const [savingNode, setSavingNode] = useState(false)
  const [confirmDeleteNodeId, setConfirmDeleteNodeId] = useState<string | null>(null)
  const [deletingNodeId, setDeletingNodeId] = useState<string | null>(null)
  const [toastMessage, setToastMessage] = useState<string | null>(null)
  const [loadError, setLoadError] = useState<string | null>(null)

  useEffect(() => {
    let mounted = true
    listNodes()
      .then((items) => {
        if (mounted) setNodes(items)
      })
      .catch((error: unknown) => {
        if (mounted) setLoadError(describeLoadError(error))
      })
    return () => {
      mounted = false
    }
  }, [])

  const confirmingNode = nodes.find((node) => node.id === confirmNodeId) ?? null
  const confirmingDeleteNode = nodes.find((node) => node.id === confirmDeleteNodeId) ?? null

  const handleCheck = async (nodeId: string) => {
    setCheckingNodeId(nodeId)
    try {
      const { latency_ms } = await healthcheckNode(nodeId)
      const latencyMs = latency_ms > 0 ? latency_ms : null
      setNodes((prev) => prev.map((n) => (n.id === nodeId ? { ...n, latencyMs, status: 'active' } : n)))
      setToastMessage(latencyMs == null ? 'Latency unavailable' : `Latency: ${latencyMs} ms`)
    } catch {
      setNodes((prev) => prev.map((n) => (n.id === nodeId ? { ...n, status: 'unreachable' } : n)))
      setToastMessage('Healthcheck failed')
    } finally {
      setCheckingNodeId(null)
    }
  }

  const handleCheckAll = async () => {
    if (nodes.length === 0) {
      setToastMessage('No nodes to check')
      return
    }
    setCheckingAll(true)
    try {
      // Every entry resolves (never rejects) so the id travels with the
      // result and survives even if `nodes` mutates (e.g. a delete) before
      // this batch settles — matching results back to rows by id, not by
      // array position, is what keeps a mid-batch delete from applying one
      // node's healthcheck result to a different node (M-R2).
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
      setNodes((prev) =>
        prev.map((node) => {
          const result = results.find((r) => r.status === 'fulfilled' && r.value.id === node.id)
          if (result?.status === 'fulfilled') {
            return { ...node, latencyMs: result.value.latencyMs, status: result.value.status }
          }
          return node
        }),
      )
      const alive = results.filter((r) => r.status === 'fulfilled' && r.value.status === 'active').length
      setToastMessage(`Checked ${results.length} nodes, ${alive} alive`)
    } finally {
      setCheckingAll(false)
    }
  }

  const handleEdit = (node: Node) => {
    setEditingNode(node)
    setEditValues({
      label: node.label,
      admin_host: node.adminHost,
      vpn_host: node.vpnHost,
      zone: node.zone,
    })
  }

  const handleSaveEdit = async () => {
    if (!editingNode) return
    setSavingNode(true)
    try {
      await patchNode(editingNode.id, editValues)
      setNodes((prev) =>
        prev.map((n) =>
          n.id === editingNode.id
            ? {
                ...n,
                label: editValues.label ?? n.label,
                adminHost: editValues.admin_host ?? n.adminHost,
                vpnHost: editValues.vpn_host ?? n.vpnHost,
                zone: editValues.zone ?? n.zone,
              }
            : n,
        ),
      )
      setEditingNode(null)
      setToastMessage('Node updated')
    } catch {
      setToastMessage('Failed to update node')
    } finally {
      setSavingNode(false)
    }
  }

  const handleConfirmRotate = async () => {
    if (confirmNodeId == null) return
    const nodeId = confirmNodeId
    setConfirmNodeId(null)
    setRotatingNodeId(nodeId)

    try {
      const { vpnHost, hy2Host, hy2Port, publicIp } = await rotateNode(nodeId)
      setNodes((prevNodes) =>
        prevNodes.map((node) =>
          node.id === nodeId
            ? {
                ...node,
                vpnHost,
                hy2Host: hy2Host ?? node.hy2Host ?? null,
                hy2Port: hy2Port ?? node.hy2Port ?? null,
                publicIp: publicIp ?? node.publicIp ?? null,
              }
            : node,
        ),
      )
      setToastMessage('Rotated successfully')
    } catch {
      setToastMessage('Rotate failed')
    } finally {
      setRotatingNodeId(null)
    }
  }

  const handleConfirmDelete = async () => {
    if (confirmDeleteNodeId == null) return
    const nodeId = confirmDeleteNodeId
    setConfirmDeleteNodeId(null)
    setDeletingNodeId(nodeId)
    try {
      const { warnings } = await deleteNode(nodeId)
      setNodes((prev) => prev.filter((n) => n.id !== nodeId))
      if (warnings.length > 0) {
        setToastMessage(`Node deleted (${warnings.length} warning${warnings.length > 1 ? 's' : ''}): ${warnings[0]}`)
      } else {
        setToastMessage('Node deleted')
      }
    } catch {
      setToastMessage('Delete failed')
    } finally {
      setDeletingNodeId(null)
    }
  }

  return (
    <>
      <section className="space-y-3">
        <div className="flex flex-wrap items-center justify-between gap-3">
          <div>
            <h1 className="text-xl font-semibold">Nodes</h1>
            <p className="mt-1 text-sm text-slate-400">Manage node hosts and run latency checks.</p>
          </div>
          <button
            type="button"
            disabled={checkingAll || checkingNodeId != null}
            onClick={() => void handleCheckAll()}
            className="rounded bg-indigo-600 px-3 py-1.5 text-xs text-white disabled:cursor-not-allowed disabled:opacity-60"
          >
            {checkingAll ? 'Checking all...' : 'Check all'}
          </button>
        </div>
        <ErrorBanner message={loadError} />
        <div className="overflow-x-auto rounded-lg border border-slate-800">
          <table className="w-full min-w-[640px] text-sm">
            <thead className="bg-slate-900 text-slate-400">
              <tr>
                <th className="px-4 py-2 text-left">ID</th>
                <th className="px-4 py-2 text-left">Name</th>
                <th className="px-4 py-2 text-left">Info</th>
                <th className="px-4 py-2 text-left">Latency</th>
                <th className="px-4 py-2 text-left">Status</th>
                <th className="px-4 py-2"></th>
              </tr>
            </thead>
            <tbody>
              {nodes.map((node) => (
                <tr key={node.id} className="border-t border-slate-800">
                  <td className="px-4 py-2 font-mono text-xs text-slate-400">{node.id}</td>
                  <td className="px-4 py-2 text-slate-100">{node.label}</td>
                  <td className="px-4 py-2 text-xs text-slate-400">
                    <div className="font-mono">{node.vpnHost}</div>
                    {node.hy2Host && <div className="font-mono">HY2 {node.hy2Host}:{node.hy2Port ?? 'N/A'}</div>}
                    {node.publicIp && <div className="font-mono">IP {node.publicIp}</div>}
                    <div className="font-mono">Mode {node.mode ?? 'direct'}</div>
                  </td>
                  <td className="px-4 py-2 text-slate-300">
                    {checkingNodeId === node.id
                      ? '...'
                      : node.latencyMs == null || node.latencyMs <= 0
                        ? 'N/A'
                        : `${node.latencyMs} ms`}
                  </td>
                  <td className="px-4 py-2">
                    <span
                      className={`rounded px-2 py-0.5 text-xs ${
                        node.status === 'active'
                          ? 'bg-green-900 text-green-300'
                          : node.status === 'degraded'
                            ? 'bg-yellow-900 text-yellow-300'
                            : node.status === 'down' || node.status === 'unreachable'
                              ? 'bg-red-900 text-red-300'
                              : 'bg-slate-800 text-slate-400'
                      }`}
                    >
                      {node.status}
                    </span>
                  </td>
                  <td className="px-4 py-2">
                    <div className="flex gap-1">
                      <button
                        type="button"
                        disabled={rotatingNodeId != null}
                        onClick={() => setConfirmNodeId(node.id)}
                        className="rounded bg-indigo-500 px-2 py-1 text-xs text-white disabled:cursor-not-allowed disabled:opacity-50"
                      >
                        {rotatingNodeId === node.id ? 'Rotating...' : 'Rotate'}
                      </button>
                      <button
                        type="button"
                        disabled={checkingAll || checkingNodeId != null}
                        onClick={() => void handleCheck(node.id)}
                        className="rounded bg-slate-800 px-2 py-1 text-xs text-slate-300 disabled:cursor-not-allowed disabled:opacity-50"
                      >
                        {checkingNodeId === node.id ? 'Checking...' : 'Check'}
                      </button>
                      <button
                        type="button"
                        onClick={() => handleEdit(node)}
                        className="rounded bg-slate-800 px-2 py-1 text-xs text-slate-300"
                      >
                        Edit
                      </button>
                      <button
                        type="button"
                        disabled={deletingNodeId != null}
                        onClick={() => setConfirmDeleteNodeId(node.id)}
                        className="rounded bg-red-900 px-2 py-1 text-xs text-red-300 disabled:cursor-not-allowed disabled:opacity-50"
                      >
                        {deletingNodeId === node.id ? 'Deleting...' : 'Delete'}
                      </button>
                    </div>
                  </td>
                </tr>
              ))}
              {nodes.length === 0 && (
                <tr>
                  <td colSpan={6} className="px-4 py-8 text-center text-slate-500">
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
        confirming={rotatingNodeId != null}
        onConfirm={() => {
          void handleConfirmRotate()
        }}
        onCancel={() => setConfirmNodeId(null)}
      />

      <ConfirmDialog
        open={confirmDeleteNodeId != null}
        confirming={deletingNodeId != null}
        title={`Delete ${confirmingDeleteNode?.label ?? 'node'}?`}
        message="This permanently removes the node from the panel. The agent service is not touched."
        confirmLabel="Confirm delete"
        onConfirm={() => {
          void handleConfirmDelete()
        }}
        onCancel={() => setConfirmDeleteNodeId(null)}
      />

      {editingNode && (
        <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/60 p-4">
          <div className="w-full max-w-sm rounded-xl border border-slate-700 bg-slate-900 p-4">
            <h2 className="text-lg font-semibold text-slate-100">Edit Node</h2>
            <div className="mt-4 space-y-3">
              <div>
                <label className="mb-1 block text-xs text-slate-400">Label</label>
                <input
                  type="text"
                  value={editValues.label ?? ''}
                  onChange={(e) => setEditValues((v) => ({ ...v, label: e.target.value }))}
                  className="w-full rounded border border-slate-700 bg-slate-800 px-3 py-2 text-sm text-slate-100"
                />
              </div>
              <div>
                <label className="mb-1 block text-xs text-slate-400">Admin Host</label>
                <input
                  type="text"
                  value={editValues.admin_host ?? ''}
                  onChange={(e) => setEditValues((v) => ({ ...v, admin_host: e.target.value }))}
                  className="w-full rounded border border-slate-700 bg-slate-800 px-3 py-2 text-sm text-slate-100"
                />
              </div>
              <div>
                <label className="mb-1 block text-xs text-slate-400">VPN Host</label>
                <input
                  type="text"
                  value={editValues.vpn_host ?? ''}
                  onChange={(e) => setEditValues((v) => ({ ...v, vpn_host: e.target.value }))}
                  className="w-full rounded border border-slate-700 bg-slate-800 px-3 py-2 text-sm text-slate-100"
                />
              </div>
              <div>
                <label className="mb-1 block text-xs text-slate-400">Zone</label>
                <input
                  type="text"
                  value={editValues.zone ?? ''}
                  onChange={(e) => setEditValues((v) => ({ ...v, zone: e.target.value }))}
                  className="w-full rounded border border-slate-700 bg-slate-800 px-3 py-2 text-sm text-slate-100"
                />
              </div>
              <div className="flex justify-end gap-2 pt-1">
                <button
                  type="button"
                  onClick={() => setEditingNode(null)}
                  className="rounded bg-slate-700 px-3 py-1.5 text-sm text-slate-100"
                >
                  Cancel
                </button>
                <button
                  type="button"
                  disabled={savingNode}
                  onClick={() => void handleSaveEdit()}
                  className="rounded bg-indigo-500 px-3 py-1.5 text-sm text-white disabled:opacity-50"
                >
                  {savingNode ? 'Saving...' : 'Save'}
                </button>
              </div>
            </div>
          </div>
        </div>
      )}

      <Toast message={toastMessage} onClose={() => setToastMessage(null)} />
    </>
  )
}
