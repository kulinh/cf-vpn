import { useEffect, useMemo, useState } from 'react'
import { QrModal } from '../components/users/QrModal'
import { Toast } from '../components/ui/Toast'
import { getUserSubscription, listNodes, listUsers, upgradeUserNodes } from '../lib/api'
import type { Node, User } from '../lib/types'

function normalizeNodeId(id: string): string {
  return id.trim().toLowerCase()
}

export function UsersPage() {
  const [users, setUsers] = useState<User[]>([])
  const [nodes, setNodes] = useState<Node[]>([])
  const [syncingUserId, setSyncingUserId] = useState<string | null>(null)
  const [qrUserId, setQrUserId] = useState<string | null>(null)
  const [qrSubscriptionUrl, setQrSubscriptionUrl] = useState<string | null>(null)
  const [qrLoading, setQrLoading] = useState(false)
  const [toastMessage, setToastMessage] = useState<string | null>(null)

  useEffect(() => {
    let mounted = true

    void Promise.all([listUsers(), listNodes()]).then(([userItems, nodeItems]) => {
      if (mounted) {
        setUsers(userItems)
        setNodes(nodeItems)
      }
    })

    return () => {
      mounted = false
    }
  }, [])

  const nodeKeys = useMemo(() => nodes.map((node) => normalizeNodeId(node.id)), [nodes])

  const missingByUser = useMemo(() => {
    return users.reduce<Record<string, number>>((acc, user) => {
      const userNodeSet = new Set(user.nodes.map(normalizeNodeId))
      acc[user.id] = nodeKeys.reduce((count, nodeKey) => (userNodeSet.has(nodeKey) ? count : count + 1), 0)
      return acc
    }, {})
  }, [nodeKeys, users])

  const handleSync = async (userId: string) => {
    setSyncingUserId(userId)

    try {
      const result = await upgradeUserNodes(userId)

      setUsers((prevUsers) =>
        prevUsers.map((user) =>
          user.id === userId
            ? {
                ...user,
                nodes: [...new Set([...user.nodes, ...result.addedNodes])],
              }
            : user,
        ),
      )

      setToastMessage(result.addedCount > 0 ? `Added ${result.addedCount} nodes` : 'User is already up-to-date')
    } catch {
      setToastMessage('Sync failed')
    } finally {
      setSyncingUserId(null)
    }
  }

  const handleCopySubscription = async (userId: string) => {
    try {
      const url = await getUserSubscription(userId)
      await navigator.clipboard.writeText(url)
      setToastMessage('Subscription URL copied!')
    } catch {
      setToastMessage('Failed to copy subscription')
    }
  }

  const handleShowQr = async (userId: string) => {
    setQrUserId(userId)
    setQrSubscriptionUrl(null)
    setQrLoading(true)
    try {
      const url = await getUserSubscription(userId)
      setQrSubscriptionUrl(url)
    } catch {
      setToastMessage('Failed to load subscription')
      setQrUserId(null)
    } finally {
      setQrLoading(false)
    }
  }

  return (
    <>
      <section className="space-y-3">
        <h1 className="text-xl font-semibold">Users</h1>
        {users.map((user) => {
          const missingCount = missingByUser[user.id] ?? 0
          const isSyncing = syncingUserId === user.id
          const isUpToDate = missingCount === 0

          return (
            <article
              key={user.id}
              className="rounded-lg border border-slate-800 bg-slate-900 p-3"
            >
              <p className="font-medium text-slate-100">{user.name}</p>
              <p className="mt-1 text-xs text-slate-400">Nodes: {user.nodes.join(', ')}</p>
              <div className="mt-2 flex flex-wrap gap-2">
                <button
                  type="button"
                  onClick={() => void handleCopySubscription(user.id)}
                  className="rounded bg-slate-700 px-3 py-1 text-xs text-slate-100"
                >
                  Copy subscription
                </button>
                <button
                  type="button"
                  onClick={() => void handleShowQr(user.id)}
                  className="rounded bg-slate-700 px-3 py-1 text-xs text-slate-100"
                >
                  Show QR
                </button>
                <button
                  type="button"
                  disabled={isSyncing || isUpToDate}
                  onClick={() => void handleSync(user.id)}
                  className="rounded bg-indigo-500 px-3 py-1 text-xs text-white disabled:cursor-not-allowed disabled:opacity-60"
                >
                  {isSyncing ? 'Syncing...' : isUpToDate ? 'Up-to-date' : `Sync (+${missingCount})`}
                </button>
              </div>
            </article>
          )
        })}
      </section>

      <Toast message={toastMessage} onClose={() => setToastMessage(null)} />

      {qrLoading ? (
        <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/60 p-4">
          <div className="rounded-xl border border-slate-700 bg-slate-900 p-6 text-slate-100">Loading...</div>
        </div>
      ) : (
        <QrModal
          open={qrUserId != null}
          userId={qrUserId}
          subscriptionUrl={qrSubscriptionUrl}
          onClose={() => setQrUserId(null)}
        />
      )}
    </>
  )
}
