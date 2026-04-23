import { FormEvent, useState } from 'react'
import { createNode } from '../lib/api'

export function AddNodePage() {
  const [id, setId] = useState('')
  const [label, setLabel] = useState('')
  const [adminHost, setAdminHost] = useState('')
  const [vpnHost, setVpnHost] = useState('')
  const [zone, setZone] = useState('')
  const [message, setMessage] = useState<string | null>(null)

  const handleSubmit = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault()
    await createNode({ id, label, adminHost, vpnHost, zone })
    setMessage('Node added')
    setId('')
    setLabel('')
    setAdminHost('')
    setVpnHost('')
    setZone('')
  }

  return (
    <section className="space-y-4">
      <h1 className="text-xl font-semibold">Add nodes</h1>
      <form className="space-y-3 rounded-lg border border-slate-800 bg-slate-900 p-4" onSubmit={(event) => void handleSubmit(event)}>
        <label className="block text-sm text-slate-200">
          ID
          <input
            className="mt-1 w-full rounded border border-slate-700 bg-slate-800 px-3 py-2 text-slate-100"
            value={id}
            onChange={(event) => setId(event.target.value)}
          />
        </label>
        <label className="block text-sm text-slate-200">
          Label
          <input
            className="mt-1 w-full rounded border border-slate-700 bg-slate-800 px-3 py-2 text-slate-100"
            value={label}
            onChange={(event) => setLabel(event.target.value)}
          />
        </label>
        <label className="block text-sm text-slate-200">
          Admin host
          <input
            className="mt-1 w-full rounded border border-slate-700 bg-slate-800 px-3 py-2 text-slate-100"
            value={adminHost}
            onChange={(event) => setAdminHost(event.target.value)}
          />
        </label>
        <label className="block text-sm text-slate-200">
          VPN host
          <input
            className="mt-1 w-full rounded border border-slate-700 bg-slate-800 px-3 py-2 text-slate-100"
            value={vpnHost}
            onChange={(event) => setVpnHost(event.target.value)}
          />
        </label>
        <label className="block text-sm text-slate-200">
          Zone
          <input
            className="mt-1 w-full rounded border border-slate-700 bg-slate-800 px-3 py-2 text-slate-100"
            value={zone}
            onChange={(event) => setZone(event.target.value)}
          />
        </label>
        <button type="submit" className="rounded bg-indigo-500 px-3 py-1.5 text-sm font-medium text-white">
          Add node
        </button>
      </form>
      {message ? <p className="text-sm text-green-400">{message}</p> : null}
    </section>
  )
}
