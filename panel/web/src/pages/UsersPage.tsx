import { useEffect, useMemo, useState } from 'react'
import { ClientBlock, LinkRow, QrPopup } from '../components/users/LinkRow'
import { ErrorBanner } from '../components/ui/ErrorBanner'
import { Toast } from '../components/ui/Toast'
import { getUserSubscription, listNodes, listUsers, upgradeUserNodes } from '../lib/api'
import { describeLoadError } from '../lib/errors'
import { RULE_SETS, buildHiddifyDeepLink, buildShadowrocketConfUrl, buildShadowrocketDeepLink, buildSingboxDeepLink, profileName } from '../lib/subscriptionLinks'
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
  const [toastMessage, setToastMessage] = useState<string | null>(null)
  const [qr, setQr] = useState<{ value: string; title: string } | null>(null)
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

  const handleCopy = async (value: string, label: string) => {
    try {
      await navigator.clipboard.writeText(value)
      setToastMessage(`${label} link copied`)
    } catch {
      setToastMessage('Failed to copy')
    }
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
          const sub = subs[user.id]

          return (
            <article
              key={user.id}
              className="rounded-lg border border-slate-800 bg-slate-900 p-3"
            >
              <p className="font-medium text-slate-100">{user.name}</p>
              <p className="mt-1 text-xs text-slate-400">Nodes: {user.nodes.join(', ')}</p>
              {sub ? (
                <>
                  <ClientBlock title="Shadowrocket" accent="text-sky-400">
                    <LinkRow
                      label="Subscription RWL"
                      fullLabel="Shadowrocket subscription RWL"
                      copyValue={sub.subUrl}
                      openHref={buildShadowrocketDeepLink(sub.subUrl)}
                      qrValue={buildShadowrocketDeepLink(sub.subUrl)}
                      onOpen={openDeepLink}
                      onCopy={handleCopy}
                      onShowQr={(value, title) => setQr({ value, title })}
                    />
                    {RULE_SETS.map((r) => (
                      <LinkRow
                        key={`sr-${r.key}`}
                        label={`Config ${profileName(r.key)}`}
                        fullLabel={`Shadowrocket config ${profileName(r.key)}`}
                        copyValue={buildShadowrocketConfUrl(sub.subUrl, r.key)}
                        onCopy={handleCopy}
                        onShowQr={(value, title) => setQr({ value, title })}
                      />
                    ))}
                  </ClientBlock>
                  <ClientBlock title="Hiddify" accent="text-emerald-400">
                    <LinkRow
                      label="Import"
                      fullLabel="Hiddify import"
                      copyValue={buildHiddifyDeepLink(sub.subUrl)}
                      openHref={buildHiddifyDeepLink(sub.subUrl)}
                      onOpen={openDeepLink}
                      onCopy={handleCopy}
                      onShowQr={(value, title) => setQr({ value, title })}
                    />
                  </ClientBlock>
                  <ClientBlock title="sing-box" accent="text-violet-400">
                    {RULE_SETS.map((r) => (
                      <LinkRow
                        key={`sb-${r.key}`}
                        label={profileName(r.key)}
                        fullLabel={`sing-box ${profileName(r.key)}`}
                        copyValue={buildSingboxDeepLink(sub.subUrl, r.key)}
                        openHref={buildSingboxDeepLink(sub.subUrl, r.key)}
                        onOpen={openDeepLink}
                        onCopy={handleCopy}
                        onShowQr={(value, title) => setQr({ value, title })}
                      />
                    ))}
                  </ClientBlock>
                </>
              ) : (
                <p className="mt-2 text-xs text-slate-500">Subscription not ready yet, please retry</p>
              )}
              <div className="mt-3 flex flex-wrap gap-2">
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
      <QrPopup value={qr?.value ?? null} title={qr?.title ?? ''} onClose={() => setQr(null)} />

    </>
  )
}
