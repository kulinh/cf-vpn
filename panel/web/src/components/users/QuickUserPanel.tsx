import { useMemo, useState } from 'react'
import type { User } from '../../lib/types'

type QuickUserPanelProps = {
  users: User[]
  onCopy: (userId: string) => void
  onShowQr: (userId: string) => void
  desktopOnly?: boolean
}

export function QuickUserPanel({
  users,
  onCopy,
  onShowQr,
  desktopOnly = true,
}: QuickUserPanelProps) {
  const [query, setQuery] = useState('')

  const filteredUsers = useMemo(() => {
    const normalizedQuery = query.trim().toLowerCase()
    if (!normalizedQuery) {
      return users
    }

    return users.filter((user) => user.name.toLowerCase().includes(normalizedQuery))
  }, [query, users])

  return (
    <aside
      className={`${desktopOnly ? 'hidden lg:block' : 'block'} w-full shrink-0 rounded-xl border border-slate-800 bg-slate-900 p-4 lg:w-80`}
    >
      <h2 className="text-sm font-semibold text-slate-200">Quick Users</h2>
      <input
        type="text"
        placeholder="Search user"
        value={query}
        onChange={(event) => setQuery(event.target.value)}
        className="mt-3 w-full rounded-md border border-slate-700 bg-slate-800 p-2 text-sm text-slate-100 outline-none focus:border-indigo-400"
      />

      <ul className="mt-3 space-y-2">
        {filteredUsers.map((user) => (
          <li key={user.id} className="rounded-lg border border-slate-700 bg-slate-800 p-3">
            <p className="font-medium text-slate-100">{user.name}</p>
            <p className="mt-1 text-xs text-slate-400">Nodes: {user.nodes.join(', ')}</p>
            <div className="mt-2 flex flex-wrap gap-2">
              <button
                type="button"
                onClick={() => onCopy(user.id)}
                className="rounded bg-slate-700 px-2.5 py-1 text-xs text-slate-100"
              >
                Copy subscription
              </button>
              <button
                type="button"
                onClick={() => onShowQr(user.id)}
                className="rounded bg-slate-700 px-2.5 py-1 text-xs text-slate-100"
              >
                Show QR
              </button>
            </div>
          </li>
        ))}
      </ul>
    </aside>
  )
}
