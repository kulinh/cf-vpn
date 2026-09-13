import { useEffect, useMemo, useRef, useState } from 'react'
import { QrModal } from '../components/users/QrModal'
import { ErrorBanner } from '../components/ui/ErrorBanner'
import { Toast } from '../components/ui/Toast'
import { getUserSubscription, listNodes, listUsers, upgradeUserNodes } from '../lib/api'
import { describeLoadError } from '../lib/errors'
import { buildHiddifyDeepLink, buildShadowrocketDeepLink, buildSingboxDeepLink } from '../lib/subscriptionLinks'
import type { UserSubscription } from '../lib/api'
import type { Node, User } from '../lib/types'

function normalizeNodeId(id: string): string {
  return id.trim().toLowerCase()
}

// Navigates to a custom-scheme deep link (shadowrocket://, hiddify://, sing-box://) via a
// synthetic <a> click rather than `location.href = ...`. Assigning
// `location.href` performs a real navigation that can leave the bearer sub
// token sitting in history / session-restore; a detached anchor click still
// hands the URL to the OS for scheme dispatch without navigating this tab.
function openDeepLink(url: string): void {
  const anchor = document.createElement('a')
  anchor.href = url
  anchor.rel = 'noopener'
  anchor.click()
}

export function UsersPage() {
  const [users, setUsers] = useState<User[]>([])
  const [nodes, setNodes] = useState<Node[]>([])
  const [subs, setSubs] = useState<Record<string, UserSubscription>>({})
  const [syncingUserId, setSyncingUserId] = useState<string | null>(null)
  const [qrUserId, setQrUserId] = useState<string | null>(null)
  const [qrSubscriptionUrl, setQrSubscriptionUrl] = useState<string | null>(null)
  const [qrLoading, setQrLoading] = useState(false)
  const [toastMessage, setToastMessage] = useState<string | null>(null)
  const [loadError, setLoadError] = useState<string | null>(null)

  useEffect(() => {
    let mounted = true

    Promise.all([listUsers(), listNodes()])
      .then(async ([userItems, nodeItems]) => {
        if (!mounted) return
        setUsers(userItems)
        setNodes(nodeItems)

        const entries = await Promise.all(
          userItems.map(async (user) => {
            try {
              const sub = await getUserSubscription(user.id)
              return [user.id, sub] as const
            } catch {
              return null
            }
          }),
        )
        if (!mounted) return
        setSubs(Object.fromEntries(entries.filter((e): e is NonNullable<typeof e> => e !== null)))
      })
      .catch((error: unknown) => {
        if (mounted) setLoadError(describeLoadError(error))
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

      setToastMessage(
        result.addedCount > 0 || (result.failedCount ?? 0) > 0
          ? `Added ${result.addedCount} nodes, failed ${result.failedCount ?? 0}, total ${result.totalNodesAfterUpgrade}`
          : 'User is already up-to-date',
      )
    } catch {
      setToastMessage('Sync failed')
    } finally {
      setSyncingUserId(null)
    }
  }

  const handleCopySubscription = async (userId: string) => {
    try {
      const sub = subs[userId] ?? (await getUserSubscription(userId))
      await navigator.clipboard.writeText(sub.subUrl)
      setToastMessage('Subscription URL copied!')
    } catch {
      setToastMessage('Failed to copy subscription')
    }
  }

  // Each Show QR click (and each close) bumps this id; a subscription fetch
  // only applies its result if it is still the latest request. Without it, a
  // slow response for user A landing after a click on user B would render B's
  // name next to A's QR / sub token.
  const qrRequestIdRef = useRef(0)

  const handleShowQr = async (userId: string) => {
    const requestId = ++qrRequestIdRef.current
    setQrUserId(userId)
    setQrSubscriptionUrl(null)
    const cached = subs[userId]
    if (cached) {
      setQrLoading(false)
      setQrSubscriptionUrl(cached.subUrl)
      return
    }
    setQrLoading(true)
    try {
      const sub = await getUserSubscription(userId)
      if (requestId !== qrRequestIdRef.current) return
      setQrSubscriptionUrl(sub.subUrl)
    } catch {
      if (requestId !== qrRequestIdRef.current) return
      setToastMessage('Failed to load subscription')
      setQrUserId(null)
    } finally {
      if (requestId === qrRequestIdRef.current) setQrLoading(false)
    }
  }

  const handleCloseQr = () => {
    qrRequestIdRef.current += 1
    setQrUserId(null)
    setQrSubscriptionUrl(null)
    setQrLoading(false)
  }

  const handleShadowrocket = (userId: string) => {
    const sub = subs[userId]
    if (!sub) {
      setToastMessage('Subscription not ready yet, please retry')
      return
    }
    openDeepLink(buildShadowrocketDeepLink(sub.subUrl))
  }

  const handleHiddify = (userId: string) => {
    const sub = subs[userId]
    if (!sub) {
      setToastMessage('Subscription not ready yet, please retry')
      return
    }
    openDeepLink(buildHiddifyDeepLink(sub.subUrl))
  }

  const handleSingbox = (userId: string) => {
    const sub = subs[userId]
    if (!sub) {
      setToastMessage('Subscription not ready yet, please retry')
      return
    }
    openDeepLink(buildSingboxDeepLink(sub.subUrl))
  }

  return (
    <>
      <section className="space-y-3">
        <h1 className="text-xl font-semibold">Users</h1>
        <ErrorBanner message={loadError} />
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
                  onClick={() => void handleShadowrocket(user.id)}
                  className="rounded bg-sky-600 px-3 py-1 text-xs text-white"
                >
                  Shadowrocket
                </button>
                <button
                  type="button"
                  onClick={() => void handleHiddify(user.id)}
                  className="rounded bg-emerald-600 px-3 py-1 text-xs text-white"
                >
                  Hiddify
                </button>
                <button
                  type="button"
                  onClick={() => void handleSingbox(user.id)}
                  title="sing-box app with rules: only blocked sites go through the proxy"
                  className="rounded bg-violet-600 px-3 py-1 text-xs text-white"
                >
                  sing-box (rules)
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
          onClose={handleCloseQr}
        />
      )}
    </>
  )
}
