import { useState } from 'react'
import { Toast } from '../components/ui/Toast'
import { createNode, createUser } from '../lib/api'

const ADMIN_HOST_SUFFIXES = [
  'rwl265.com',
  '888vn.net',
  'dongnat247.com',
  'rwl247.dev',
  'rwl265.org',
]

function randomHex(bytes: number): string {
  const arr = new Uint8Array(bytes)
  crypto.getRandomValues(arr)
  return Array.from(arr)
    .map((b) => b.toString(16).padStart(2, '0'))
    .join('')
}

function generateRandomVpnHost(): string {
  const hex = randomHex(4)
  const suffix = ADMIN_HOST_SUFFIXES[Math.floor(Math.random() * ADMIN_HOST_SUFFIXES.length)]
  return `${hex}.${suffix}`
}

function generateRandomAdminHost(): string {
  const hex = randomHex(4)
  const suffix = ADMIN_HOST_SUFFIXES[Math.floor(Math.random() * ADMIN_HOST_SUFFIXES.length)]
  return `${hex}.${suffix}`
}

export function QuickAddPage() {
  const [nodeId, setNodeId] = useState('')
  const [nodeLabel, setNodeLabel] = useState('')
  const [nodeAdminHost, setNodeAdminHost] = useState('')
  const [nodeVpnHost, setNodeVpnHost] = useState('')
  const [nodeZone, setNodeZone] = useState('')
  const [submittingNode, setSubmittingNode] = useState(false)

  const [userName, setUserName] = useState('')
  const [submittingUser, setSubmittingUser] = useState(false)

  const [toastMessage, setToastMessage] = useState<string | null>(null)

  const handleAddNode = async () => {
    if (!nodeId || !nodeLabel || !nodeAdminHost || !nodeVpnHost || !nodeZone) {
      setToastMessage('All node fields are required')
      return
    }
    setSubmittingNode(true)
    try {
      await createNode({
        id: nodeId.trim(),
        label: nodeLabel.trim(),
        admin_host: nodeAdminHost.trim(),
        vpn_host: nodeVpnHost.trim(),
        zone: nodeZone.trim(),
      })
      setNodeId('')
      setNodeLabel('')
      setNodeAdminHost('')
      setNodeVpnHost('')
      setNodeZone('')
      setToastMessage('Node added successfully')
    } catch {
      setToastMessage('Failed to add node')
    } finally {
      setSubmittingNode(false)
    }
  }

  const handleAddUser = async () => {
    if (!userName.trim()) {
      setToastMessage('User name is required')
      return
    }
    setSubmittingUser(true)
    try {
      await createUser({ name: userName.trim() })
      setUserName('')
      setToastMessage('User added successfully')
    } catch {
      setToastMessage('Failed to add user')
    } finally {
      setSubmittingUser(false)
    }
  }

  return (
    <>
      <section className="space-y-6">
        <h1 className="text-xl font-semibold">Quick Add</h1>

        <div className="grid gap-6 md:grid-cols-2">
          <div className="rounded-lg border border-slate-800 bg-slate-900 p-4">
            <h2 className="mb-4 text-lg font-medium">Add Node</h2>
            <div className="space-y-3">
              <div>
                <label className="mb-1 block text-xs text-slate-400">ID</label>
                <input
                  type="text"
                  value={nodeId}
                  onChange={(e) => setNodeId(e.target.value)}
                  placeholder="e.g. sg-01"
                  className="w-full rounded border border-slate-700 bg-slate-800 px-3 py-2 text-sm text-slate-100 placeholder-slate-500"
                />
              </div>
              <div>
                <label className="mb-1 block text-xs text-slate-400">Label</label>
                <input
                  type="text"
                  value={nodeLabel}
                  onChange={(e) => setNodeLabel(e.target.value)}
                  placeholder="e.g. Singapore"
                  className="w-full rounded border border-slate-700 bg-slate-800 px-3 py-2 text-sm text-slate-100 placeholder-slate-500"
                />
              </div>
              <div>
                <label className="mb-1 block text-xs text-slate-400">Admin Host</label>
                <div className="flex gap-2">
                  <input
                    type="text"
                    value={nodeAdminHost}
                    onChange={(e) => setNodeAdminHost(e.target.value)}
                    placeholder="e.g. ab12cd34.rwl265.com"
                    className="flex-1 rounded border border-slate-700 bg-slate-800 px-3 py-2 text-sm text-slate-100 placeholder-slate-500"
                  />
                  <button
                    type="button"
                    onClick={() => setNodeAdminHost(generateRandomAdminHost())}
                    className="rounded bg-slate-700 px-3 py-2 text-xs text-slate-100"
                  >
                    Random
                  </button>
                </div>
              </div>
              <div>
                <label className="mb-1 block text-xs text-slate-400">VPN Host</label>
                <div className="flex gap-2">
                  <input
                    type="text"
                    value={nodeVpnHost}
                    onChange={(e) => setNodeVpnHost(e.target.value)}
                    placeholder="e.g. ab12cd34.rwl265.com"
                    className="flex-1 rounded border border-slate-700 bg-slate-800 px-3 py-2 text-sm text-slate-100 placeholder-slate-500"
                  />
                  <button
                    type="button"
                    onClick={() => setNodeVpnHost(generateRandomVpnHost())}
                    className="rounded bg-slate-700 px-3 py-2 text-xs text-slate-100"
                  >
                    Random
                  </button>
                </div>
              </div>
              <div>
                <label className="mb-1 block text-xs text-slate-400">Zone</label>
                <input
                  type="text"
                  value={nodeZone}
                  onChange={(e) => setNodeZone(e.target.value)}
                  placeholder="e.g. rwl265.com"
                  className="w-full rounded border border-slate-700 bg-slate-800 px-3 py-2 text-sm text-slate-100 placeholder-slate-500"
                />
              </div>
              <button
                type="button"
                disabled={submittingNode}
                onClick={() => void handleAddNode()}
                className="w-full rounded bg-indigo-500 py-2 text-sm font-medium text-white disabled:cursor-not-allowed disabled:opacity-50"
              >
                {submittingNode ? 'Adding...' : 'Add Node'}
              </button>
            </div>
          </div>

          <div className="rounded-lg border border-slate-800 bg-slate-900 p-4">
            <h2 className="mb-4 text-lg font-medium">Add User</h2>
            <div className="space-y-3">
              <div>
                <label className="mb-1 block text-xs text-slate-400">Name</label>
                <input
                  type="text"
                  value={userName}
                  onChange={(e) => setUserName(e.target.value)}
                  placeholder="e.g. John Doe"
                  className="w-full rounded border border-slate-700 bg-slate-800 px-3 py-2 text-sm text-slate-100 placeholder-slate-500"
                />
              </div>
              <button
                type="button"
                disabled={submittingUser}
                onClick={() => void handleAddUser()}
                className="w-full rounded bg-indigo-500 py-2 text-sm font-medium text-white disabled:cursor-not-allowed disabled:opacity-50"
              >
                {submittingUser ? 'Adding...' : 'Add User'}
              </button>
            </div>
          </div>
        </div>
      </section>

      <Toast message={toastMessage} onClose={() => setToastMessage(null)} />
    </>
  )
}
