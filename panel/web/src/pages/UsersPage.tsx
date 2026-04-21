import { useEffect, useState } from 'react'
import { listUsers } from '../lib/api'
import type { User } from '../lib/types'

export function UsersPage() {
  const [users, setUsers] = useState<User[]>([])

  useEffect(() => {
    let mounted = true

    void listUsers().then((items) => {
      if (mounted) {
        setUsers(items)
      }
    })

    return () => {
      mounted = false
    }
  }, [])

  return (
    <section className="space-y-3">
      <h1 className="text-xl font-semibold">Users</h1>
      {users.map((user) => (
        <article
          key={user.id}
          className="rounded-lg border border-slate-800 bg-slate-900 p-3"
        >
          <p className="font-medium text-slate-100">{user.name}</p>
          <p className="mt-1 text-xs text-slate-400">Nodes: {user.nodes.join(', ')}</p>
          <div className="mt-2 flex gap-2">
            <button type="button" className="rounded bg-slate-700 px-3 py-1 text-xs text-slate-100">
              Copy subscription
            </button>
            <button type="button" className="rounded bg-slate-700 px-3 py-1 text-xs text-slate-100">
              Show QR
            </button>
          </div>
        </article>
      ))}
    </section>
  )
}
