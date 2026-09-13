import { useEffect, useState } from 'react'
import { ConfirmDialog } from '../components/ui/ConfirmDialog'
import { ErrorBanner } from '../components/ui/ErrorBanner'
import { Toast } from '../components/ui/Toast'
import { Badge, LatencyText, ModeBadge, PageHeader, btnDanger, btnGhost, btnMd, btnPrimary, btnSm, card, input, label as labelCls, statusTone, tableWrap, td, th, thead, tr } from '../components/ui/theme'
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
    } catch (error) {
      setToastMessage(error instanceof Error ? error.message : 'Rotate failed')
      // The agent may have rotated the host even though the request that
      // reported it back failed (e.g. a post-rotate durability error) — or
      // it may have retried onto a different host. Re-fetch so the row
      // reflects whatever the agent actually did rather than staying stale.
      try {
        setNodes(await listNodes())
      } catch {
        // Load-failure banner isn't appropriate mid-toast; leave the stale
        // row rather than losing the rotate-failure toast to a second error.
      }
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
      <section className="space-y-4">
        <PageHeader
          title="Nodes"
          subtitle="Manage node hosts and run latency checks."
          actions={
            <button
              type="button"
              disabled={checkingAll || checkingNodeId != null}
              onClick={() => void handleCheckAll()}
              className={`${btnPrimary} ${btnMd}`}
            >
              {checkingAll ? 'Checking all...' : 'Check all'}
            </button>
          }
        />
        <ErrorBanner message={loadError} />
        <div className={tableWrap}>
          <table className="w-full min-w-[640px] text-sm">
            <thead className={thead}>
              <tr>
                <th className={th}>ID</th>
                <th className={th}>Name</th>
                <th className={th}>Info</th>
                <th className={th}>Latency</th>
                <th className={th}>Status</th>
                <th className={th}></th>
              </tr>
            </thead>
            <tbody>
              {nodes.map((node) => (
                <tr key={node.id} className={tr}>
                  <td className={`${td} whitespace-nowrap font-mono text-xs font-medium text-indigo-600 dark:text-indigo-300`}>{node.id}</td>
                  <td className={`${td} font-medium text-slate-900 dark:text-slate-100`}>{node.label}</td>
                  <td className={`${td} text-xs`}>
                    <div className="font-mono text-slate-700 dark:text-slate-300">{node.vpnHost}</div>
                    {node.hy2Host && (
                      <div className="font-mono text-slate-500 dark:text-slate-400">
                        HY2 {node.hy2Host}:{node.hy2Port ?? 'N/A'}
                      </div>
                    )}
                    {node.publicIp && <div className="font-mono text-slate-500 dark:text-slate-400">IP {node.publicIp}</div>}
                    <div className="mt-1 flex items-center gap-1 font-mono text-slate-500 dark:text-slate-400">
                      <span className="sr-only">Mode {node.mode ?? 'direct'}</span>
                      <ModeBadge mode={node.mode} />
                    </div>
                  </td>
                  <td className={td}>
                    <LatencyText ms={node.latencyMs} pending={checkingNodeId === node.id} />
                  </td>
                  <td className={td}>
                    <Badge tone={statusTone(node.status)} withDot>
                      {node.status}
                    </Badge>
                  </td>
                  <td className={td}>
                    <div className="flex justify-end gap-1.5">
                      <button
                        type="button"
                        disabled={rotatingNodeId != null}
                        onClick={() => setConfirmNodeId(node.id)}
                        className={`${btnPrimary} ${btnSm}`}
                      >
                        {rotatingNodeId === node.id ? 'Rotating...' : 'Rotate'}
                      </button>
                      <button
                        type="button"
                        disabled={checkingAll || checkingNodeId != null}
                        onClick={() => void handleCheck(node.id)}
                        className={`${btnGhost} ${btnSm}`}
                      >
                        {checkingNodeId === node.id ? 'Checking...' : 'Check'}
                      </button>
                      <button type="button" onClick={() => handleEdit(node)} className={`${btnGhost} ${btnSm}`}>
                        Edit
                      </button>
                      <button
                        type="button"
                        disabled={deletingNodeId != null}
                        onClick={() => setConfirmDeleteNodeId(node.id)}
                        className={`${btnDanger} ${btnSm}`}
                      >
                        {deletingNodeId === node.id ? 'Deleting...' : 'Delete'}
                      </button>
                    </div>
                  </td>
                </tr>
              ))}
              {nodes.length === 0 && (
                <tr>
                  <td colSpan={6} className="px-4 py-8 text-center text-slate-500 dark:text-slate-400">
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
        <div className="fixed inset-0 z-50 flex items-center justify-center bg-slate-950/50 p-4 backdrop-blur-sm">
          <div className={`${card} w-full max-w-sm p-4 shadow-xl dark:bg-slate-900`}>
            <h2 className="text-lg font-semibold text-slate-900 dark:text-slate-100">Edit Node</h2>
            <div className="mt-4 space-y-3">
              <div>
                <label className={labelCls}>Label</label>
                <input
                  type="text"
                  value={editValues.label ?? ''}
                  onChange={(e) => setEditValues((v) => ({ ...v, label: e.target.value }))}
                  className={input}
                />
              </div>
              <div>
                <label className={labelCls}>Admin Host</label>
                <input
                  type="text"
                  value={editValues.admin_host ?? ''}
                  onChange={(e) => setEditValues((v) => ({ ...v, admin_host: e.target.value }))}
                  className={input}
                />
              </div>
              <div>
                <label className={labelCls}>VPN Host</label>
                <input
                  type="text"
                  value={editValues.vpn_host ?? ''}
                  onChange={(e) => setEditValues((v) => ({ ...v, vpn_host: e.target.value }))}
                  className={input}
                />
              </div>
              <div>
                <label className={labelCls}>Zone</label>
                <input
                  type="text"
                  value={editValues.zone ?? ''}
                  onChange={(e) => setEditValues((v) => ({ ...v, zone: e.target.value }))}
                  className={input}
                />
              </div>
              <div className="flex justify-end gap-2 pt-1">
                <button
                  type="button"
                  onClick={() => setEditingNode(null)}
                  className={`${btnGhost} ${btnMd}`}
                >
                  Cancel
                </button>
                <button
                  type="button"
                  disabled={savingNode}
                  onClick={() => void handleSaveEdit()}
                  className={`${btnPrimary} ${btnMd}`}
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
