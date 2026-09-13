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
        <h1 className="text-xl font-semibold text-slate-900 dark:text-slate-100">Users</h1>
        <ErrorBanner message={loadError} />
        {users.map((user) => {
          const missingCount = missingByUser[user.id] ?? 0
          const isSyncing = syncingUserId === user.id
          const isUpToDate = missingCount === 0
          const sub = subs[user.id]

          return (
            <article
              key={user.id}
              className="rounded-xl border border-slate-200 bg-white p-3 shadow-sm dark:border-slate-800 dark:bg-slate-900/60 dark:shadow-none"
            >
              <p className="flex items-center gap-2 font-semibold text-slate-900 dark:text-slate-100">
                <span className="flex h-7 w-7 items-center justify-center rounded-full bg-gradient-to-br from-indigo-500 to-fuchsia-500 text-xs font-bold uppercase text-white" aria-hidden="true">
                  {user.name.slice(0, 1)}
                </span>
                {user.name}
              </p>
              <div className="mt-2 flex flex-wrap gap-1">
                {user.nodes.map((n) => (
                  <span key={n} className="rounded-full bg-slate-100 px-2 py-0.5 font-mono text-[11px] text-slate-600 dark:bg-slate-800 dark:text-slate-300">
                    {n}
                  </span>
                ))}
              </div>
              {sub ? (
                <div className="mt-3 grid grid-cols-1 gap-2 md:grid-cols-3">
                  <ClientBlock title="Shadowrocket" tone="sky">
                    <LinkRow
                      label="Subscription RWL"
                      fullLabel="Shadowrocket subscription RWL"
                      tone="sky"
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
                        tone="sky"
                        list={r.label}
                        copyValue={buildShadowrocketConfUrl(sub.subUrl, r.key)}
                        onCopy={handleCopy}
                        onShowQr={(value, title) => setQr({ value, title })}
                      />
                    ))}
                  </ClientBlock>
                  <ClientBlock title="Hiddify" tone="emerald">
                    <LinkRow
                      label="Import"
                      fullLabel="Hiddify import"
                      tone="emerald"
                      copyValue={buildHiddifyDeepLink(sub.subUrl)}
                      openHref={buildHiddifyDeepLink(sub.subUrl)}
                      onOpen={openDeepLink}
                      onCopy={handleCopy}
                      onShowQr={(value, title) => setQr({ value, title })}
                    />
                  </ClientBlock>
                  <ClientBlock title="sing-box" tone="violet">
                    {RULE_SETS.map((r) => (
                      <LinkRow
                        key={`sb-${r.key}`}
                        label={profileName(r.key)}
                        fullLabel={`sing-box ${profileName(r.key)}`}
                        tone="violet"
                        list={r.label}
                        copyValue={buildSingboxDeepLink(sub.subUrl, r.key)}
                        openHref={buildSingboxDeepLink(sub.subUrl, r.key)}
                        onOpen={openDeepLink}
                        onCopy={handleCopy}
                        onShowQr={(value, title) => setQr({ value, title })}
                      />
                    ))}
                  </ClientBlock>
                </div>
              ) : (
                <p className="mt-2 text-xs text-slate-500 dark:text-slate-400">Subscription not ready yet, please retry</p>
              )}
              <div className="mt-3 flex flex-wrap gap-2">
                <button
                  type="button"
                  disabled={isSyncing || isUpToDate}
                  onClick={() => void handleSync(user.id)}
                  className="rounded-md bg-indigo-600 px-3 py-1 text-xs font-semibold text-white hover:bg-indigo-700 disabled:cursor-not-allowed disabled:bg-slate-200 disabled:text-slate-500 dark:bg-indigo-500 dark:hover:bg-indigo-400 dark:disabled:bg-slate-800 dark:disabled:text-slate-500"
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
