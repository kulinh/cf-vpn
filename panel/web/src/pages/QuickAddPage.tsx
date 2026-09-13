import { useState } from 'react'
import { Toast } from '../components/ui/Toast'
import { createNode, createUser } from '../lib/api'
import { PageHeader, btnGhost, btnPrimary, card, input, label as labelCls } from '../components/ui/theme'

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

// Used for both the admin and the VPN host fields: 8 hex chars under a random suffix.
function generateRandomHost(): string {
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
        <PageHeader title="Quick Add" subtitle="Register a node or create a user." />

        <div className="grid gap-6 md:grid-cols-2">
          <div className={`${card} p-4`}>
            <h2 className="mb-4 flex items-center gap-2 text-base font-semibold text-slate-900 dark:text-slate-100"><span className="h-2 w-2 rounded-full bg-indigo-500" aria-hidden="true" />Add Node</h2>
            <div className="space-y-3">
              <div>
                <label className={labelCls}>ID</label>
                <input
                  type="text"
                  value={nodeId}
                  onChange={(e) => setNodeId(e.target.value)}
                  placeholder="e.g. sg-01"
                  className={input}
                />
              </div>
              <div>
                <label className={labelCls}>Label</label>
                <input
                  type="text"
                  value={nodeLabel}
                  onChange={(e) => setNodeLabel(e.target.value)}
                  placeholder="e.g. Singapore"
                  className={input}
                />
              </div>
              <div>
                <label className={labelCls}>Admin Host</label>
                <div className="flex gap-2">
                  <input
                    type="text"
                    value={nodeAdminHost}
                    onChange={(e) => setNodeAdminHost(e.target.value)}
                    placeholder="e.g. ab12cd34.rwl265.com"
                    className={`${input} flex-1`}
                  />
                  <button
                    type="button"
                    onClick={() => setNodeAdminHost(generateRandomHost())}
                    className={`${btnGhost} px-3 py-2 text-xs`}
                  >
                    Random
                  </button>
                </div>
              </div>
              <div>
                <label className={labelCls}>VPN Host</label>
                <div className="flex gap-2">
                  <input
                    type="text"
                    value={nodeVpnHost}
                    onChange={(e) => setNodeVpnHost(e.target.value)}
                    placeholder="e.g. ab12cd34.rwl265.com"
                    className={`${input} flex-1`}
                  />
                  <button
                    type="button"
                    onClick={() => setNodeVpnHost(generateRandomHost())}
                    className={`${btnGhost} px-3 py-2 text-xs`}
                  >
                    Random
                  </button>
                </div>
              </div>
              <div>
                <label className={labelCls}>Zone</label>
                <input
                  type="text"
                  value={nodeZone}
                  onChange={(e) => setNodeZone(e.target.value)}
                  placeholder="e.g. rwl265.com"
                  className={input}
                />
              </div>
              <button
                type="button"
                disabled={submittingNode}
                onClick={() => void handleAddNode()}
                className={`${btnPrimary} w-full py-2 text-sm`}
              >
                {submittingNode ? 'Adding...' : 'Add Node'}
              </button>
            </div>
          </div>

          <div className={`${card} p-4`}>
            <h2 className="mb-4 flex items-center gap-2 text-base font-semibold text-slate-900 dark:text-slate-100"><span className="h-2 w-2 rounded-full bg-fuchsia-500" aria-hidden="true" />Add User</h2>
            <div className="space-y-3">
              <div>
                <label className={labelCls}>Name</label>
                <input
                  type="text"
                  value={userName}
                  onChange={(e) => setUserName(e.target.value)}
                  placeholder="e.g. John Doe"
                  className={input}
                />
              </div>
              <button
                type="button"
                disabled={submittingUser}
                onClick={() => void handleAddUser()}
                className={`${btnPrimary} w-full py-2 text-sm`}
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
