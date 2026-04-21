import { useMemo, useState } from 'react'
import { NodeGrid } from '../components/nodes/NodeGrid'
import { ConfirmDialog } from '../components/ui/ConfirmDialog'
import { Toast } from '../components/ui/Toast'
import { QrModal } from '../components/users/QrModal'
import { QuickUserPanel } from '../components/users/QuickUserPanel'
import { UserBottomSheet } from '../components/users/UserBottomSheet'
import { StatusStrip } from '../components/status/StatusStrip'
import { rotateNode } from '../lib/api'
import type { Node, NodeFilter, NodeStatus, User } from '../lib/types'

const initialNodes: Node[] = [
  {
    id: 'sg',
    label: 'Singapore',
    status: 'active',
    latencyMs: 82,
    vpnHost: 'sg.example.com',
    lastSeenAt: Date.now(),
  },
  {
    id: 'jp',
    label: 'Tokyo',
    status: 'degraded',
    latencyMs: 182,
    vpnHost: 'jp.example.com',
    lastSeenAt: Date.now(),
  },
  {
    id: 'hk',
    label: 'Hong Kong',
    status: 'down',
    latencyMs: null,
    vpnHost: 'hk.example.com',
    lastSeenAt: null,
  },
]

const statusKeys: NodeStatus[] = ['active', 'degraded', 'down']

const demoUsers: User[] = [
  { id: 'kulinh', name: 'kulinh', nodes: ['SG', 'JP1'] },
  { id: 'minh', name: 'minh', nodes: ['JP', 'HK'] },
]

export function CommandCenterPage() {
  const [nodes, setNodes] = useState<Node[]>(initialNodes)
  const [filter, setFilter] = useState<NodeFilter>('all')
  const [isUserSheetOpen, setIsUserSheetOpen] = useState(false)
  const [qrUserId, setQrUserId] = useState<string | null>(null)
  const [confirmNodeId, setConfirmNodeId] = useState<string | null>(null)
  const [rotatingNodeId, setRotatingNodeId] = useState<string | null>(null)
  const [toastMessage, setToastMessage] = useState<string | null>(null)

  const handleCopySubscription = (userId: string) => {
    window.navigator.clipboard?.writeText(`https://example.com/sub/${userId}`)
  }

  const handleShowQr = (userId: string) => {
    setQrUserId(userId)
  }

  const handleCloseQr = () => {
    setQrUserId(null)
  }

  const handleCloseUserSheet = () => {
    setIsUserSheetOpen(false)
  }

  const handleOpenUserSheet = () => {
    setIsUserSheetOpen(true)
  }

  const counts = useMemo(
    () =>
      statusKeys.reduce(
        (acc, status) => ({
          ...acc,
          [status]: nodes.filter((node) => node.status === status).length,
        }),
        { active: 0, degraded: 0, down: 0 },
      ),
    [nodes],
  )

  const avgLatency = useMemo(() => {
    const latencies = nodes
      .map((node) => node.latencyMs)
      .filter((latency): latency is number => latency != null)

    if (latencies.length === 0) {
      return 0
    }

    return Math.round(latencies.reduce((sum, latency) => sum + latency, 0) / latencies.length)
  }, [nodes])

  const filteredNodes = filter === 'all' ? nodes : nodes.filter((node) => node.status === filter)
  const isMobileViewport = typeof window !== 'undefined' && window.innerWidth < 1024
  const confirmingNode = nodes.find((node) => node.id === confirmNodeId) ?? null

  const handleRotateRequest = (nodeId: string) => {
    if (rotatingNodeId != null) {
      return
    }

    setConfirmNodeId(nodeId)
  }

  const handleConfirmRotate = async () => {
    if (confirmNodeId == null) {
      return
    }

    const nodeId = confirmNodeId
    setConfirmNodeId(null)
    setRotatingNodeId(nodeId)

    try {
      const result = await rotateNode(nodeId)
      setNodes((prevNodes) =>
        prevNodes.map((node) => (node.id === nodeId ? { ...node, vpnHost: result.vpnHost } : node)),
      )
      setToastMessage('Rotated successfully')
    } catch {
      setToastMessage('Rotate failed')
    } finally {
      setRotatingNodeId(null)
    }
  }

  return (
    <>
      <div className="space-y-4">
        <StatusStrip
          active={counts.active}
          degraded={counts.degraded}
          down={counts.down}
          avgLatencyMs={avgLatency}
          filter={filter}
          onFilter={setFilter}
        />

        <section className="rounded-lg bg-slate-900 p-3 text-sm text-slate-300">
          Showing {filteredNodes.length} node{filteredNodes.length === 1 ? '' : 's'}
        </section>

        <div className="flex gap-4">
          <div className="min-w-0 flex-1">
            <NodeGrid
              nodes={filteredNodes}
              onRotate={handleRotateRequest}
              onHealthcheck={() => {}}
              onOpen={() => {}}
              rotatingNodeId={rotatingNodeId}
            />
          </div>

          <QuickUserPanel users={demoUsers} onCopy={handleCopySubscription} onShowQr={handleShowQr} />
        </div>

        {isMobileViewport ? (
          <button
            type="button"
            className="fixed bottom-4 right-4 rounded-full bg-indigo-500 px-4 py-2 text-sm font-medium text-white shadow-lg lg:hidden"
            onClick={handleOpenUserSheet}
          >
            Users
          </button>
        ) : null}
      </div>

      <UserBottomSheet open={isUserSheetOpen} onClose={handleCloseUserSheet}>
        <QuickUserPanel
          users={demoUsers}
          onCopy={handleCopySubscription}
          onShowQr={handleShowQr}
          desktopOnly={false}
        />
      </UserBottomSheet>

      <QrModal open={qrUserId != null} userId={qrUserId} onClose={handleCloseQr} />

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
