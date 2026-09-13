import { useEffect, useRef, useState } from 'react'
import { ErrorBanner } from '../components/ui/ErrorBanner'
import { Toast } from '../components/ui/Toast'
import { Badge, LatencyText, ModeBadge, PageHeader, StatCard, btnMd, btnPrimary, statusTone, tableWrap, td, th, thead, tr } from '../components/ui/theme'
import { healthcheckNode, listNodes } from '../lib/api'
import { describeLoadError } from '../lib/errors'
import type { Node } from '../lib/types'

export function CommandCenterPage() {
  const [nodes, setNodes] = useState<Node[]>([])
  const [checkingAll, setCheckingAll] = useState(false)
  const [toastMessage, setToastMessage] = useState<string | null>(null)
  const [loadError, setLoadError] = useState<string | null>(null)
  const isMountedRef = useRef(true)

  useEffect(() => {
    isMountedRef.current = true

    listNodes()
      .then((items) => {
        if (isMountedRef.current) setNodes(items)
      })
      .catch((error: unknown) => {
        if (isMountedRef.current) setLoadError(describeLoadError(error))
      })

    return () => {
      isMountedRef.current = false
    }
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
      if (!isMountedRef.current) return
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
      if (isMountedRef.current) setToastMessage('Check failed')
    } finally {
      if (isMountedRef.current) setCheckingAll(false)
    }
  }

  const STATUS_LABEL: Record<string, string> = {
    active: 'Active',
    unreachable: 'Unreachable',
    down: 'Down',
    degraded: 'Degraded',
    disabled: 'Disabled',
  }
  const getStatusBadge = (status: Node['status']) => (
    <Badge tone={statusTone(status)} withDot>
      {STATUS_LABEL[status] ?? 'Unknown'}
    </Badge>
  )

  const activeCount = nodes.filter((n) => n.status === 'active').length
  const issueCount = nodes.filter((n) => n.status === 'unreachable' || n.status === 'down' || n.status === 'degraded').length
  const cloudflareCount = nodes.filter((n) => n.mode === 'cloudflare').length

  return (
    <>
      <section className="space-y-5">
        <PageHeader
          title="Home"
          subtitle="Fleet health overview and latency snapshot."
          actions={
            <button
              type="button"
              disabled={checkingAll}
              onClick={() => void handleRefreshAll()}
              className={`${btnPrimary} ${btnMd}`}
            >
              {checkingAll ? 'Checking...' : 'Refresh All'}
            </button>
          }
        />

        <ErrorBanner message={loadError} />

        <div className="grid grid-cols-2 gap-3 md:grid-cols-4">
          <StatCard label="Nodes" value={nodes.length} accent="bg-indigo-500" />
          <StatCard label="Healthy" value={activeCount} accent="bg-emerald-500" />
          <StatCard label="Issues" value={issueCount} accent={issueCount > 0 ? 'bg-rose-500' : 'bg-slate-300 dark:bg-slate-700'} />
          <StatCard label="Via Cloudflare" value={cloudflareCount} accent="bg-orange-500" />
        </div>

        <div className={tableWrap}>
          <table className="w-full text-sm">
            <thead className={thead}>
              <tr>
                <th className={th}>ID</th>
                <th className={th}>Name</th>
                <th className={th}>Mode</th>
                <th className={th}>Latency</th>
                <th className={th}>Status</th>
              </tr>
            </thead>
            <tbody>
              {nodes.map((node) => (
                <tr key={node.id} className={tr}>
                  <td className={`${td} whitespace-nowrap font-mono text-xs font-medium text-indigo-600 dark:text-indigo-300`}>{node.id}</td>
                  <td className={`${td} font-medium text-slate-900 dark:text-slate-100`}>{node.label}</td>
                  <td className={td}>
                    <ModeBadge mode={node.mode} />
                  </td>
                  <td className={td}>
                    <LatencyText ms={node.latencyMs} />
                  </td>
                  <td className={td}>{getStatusBadge(node.status)}</td>
                </tr>
              ))}
              {nodes.length === 0 && (
                <tr>
                  <td colSpan={5} className="px-4 py-8 text-center text-slate-500 dark:text-slate-400">
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
