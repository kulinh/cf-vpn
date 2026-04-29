# cf-vpn Control Panel UI Redesign Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Ship a UI-only redesign of the cf-vpn control panel using the approved Node Command Center UX, while keeping existing backend/API behavior unchanged.

**Architecture:** Build a React + TypeScript SPA in `panel/web` with a command-center-first layout, dark NOC visual system, responsive desktop/mobile behavior, and API adapters that call existing `/api/*` endpoints. Keep state local to pages/components and use a small service layer (`lib/api.ts`) for fetch calls so UI behavior is testable without backend changes.

**Tech Stack:** Vite, React, TypeScript, React Router, Tailwind CSS, Vitest + Testing Library, Playwright (smoke UI only)

---

## Scope Check

This spec is one subsystem (frontend UI redesign) and does not require splitting into separate plans.

---

## File Structure

**Create:**
- `panel/web/package.json` — frontend dependencies and scripts
- `panel/web/vite.config.ts` — Vite config
- `panel/web/tsconfig.json` — TypeScript config
- `panel/web/index.html` — app mount point
- `panel/web/src/main.tsx` — SPA bootstrap
- `panel/web/src/app/App.tsx` — route shell
- `panel/web/src/app/Layout.tsx` — top nav + shared layout
- `panel/web/src/styles/tailwind.css` — Tailwind base + design tokens
- `panel/web/src/lib/types.ts` — typed models (`Node`, `User`, `Event`)
- `panel/web/src/lib/api.ts` — API client wrappers over existing `/api/*`
- `panel/web/src/lib/format.ts` — display helpers (latency, timestamps)
- `panel/web/src/components/status/StatusStrip.tsx` — Active/Degraded/Down/Latency strip
- `panel/web/src/components/nodes/NodeCard.tsx` — command-center node card
- `panel/web/src/components/nodes/NodeGrid.tsx` — responsive grid + filtering
- `panel/web/src/components/users/QuickUserPanel.tsx` — sticky desktop user panel
- `panel/web/src/components/users/UserBottomSheet.tsx` — mobile user panel
- `panel/web/src/components/users/QrModal.tsx` — QR + copy modal
- `panel/web/src/components/ui/Toast.tsx` — action feedback toasts
- `panel/web/src/components/ui/ConfirmDialog.tsx` — compact rotate confirmation
- `panel/web/src/pages/CommandCenterPage.tsx` — home workspace
- `panel/web/src/pages/UsersPage.tsx` — user list + subscription actions
- `panel/web/src/pages/EventsPage.tsx` — event list page
- `panel/web/src/pages/SettingsPage.tsx` — settings access page (header entry)
- `panel/web/src/test/setup.ts` — Vitest setup
- `panel/web/src/test/fixtures.ts` — shared test fixture data
- `panel/web/src/app/App.test.tsx`
- `panel/web/src/pages/CommandCenterPage.test.tsx`
- `panel/web/src/components/nodes/NodeCard.test.tsx`
- `panel/web/src/components/users/QuickUserPanel.test.tsx`
- `panel/web/src/pages/UsersPage.test.tsx`
- `panel/web/src/pages/EventsPage.test.tsx`
- `panel/web/playwright.config.ts`
- `panel/web/e2e/command-center.spec.ts`

**Modify (during implementation):**
- `README.md` — add section for control-panel frontend dev/test commands
- `docs/TESTING.md` — add manual UI validation steps from approved spec

---

### Task 1: Bootstrap frontend workspace and route shell

**Files:**
- Create: `panel/web/package.json`, `panel/web/vite.config.ts`, `panel/web/tsconfig.json`, `panel/web/index.html`
- Create: `panel/web/src/main.tsx`, `panel/web/src/app/App.tsx`, `panel/web/src/app/Layout.tsx`, `panel/web/src/styles/tailwind.css`
- Create: `panel/web/src/app/App.test.tsx`, `panel/web/src/test/setup.ts`

- [ ] **Step 1: Write failing app-shell test**

```tsx
// panel/web/src/app/App.test.tsx
import { render, screen } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { App } from './App'

test('renders command center shell nav', () => {
  render(
    <MemoryRouter initialEntries={['/']}>
      <App />
    </MemoryRouter>,
  )

  expect(screen.getByRole('button', { name: /command center/i })).toBeInTheDocument()
  expect(screen.getByRole('button', { name: /users/i })).toBeInTheDocument()
  expect(screen.getByRole('button', { name: /events/i })).toBeInTheDocument()
})
```

- [ ] **Step 2: Run test to verify failure**

Run: `npm --prefix panel/web test -- --run App.test.tsx`
Expected: FAIL with module/file not found.

- [ ] **Step 3: Create minimal project config**

```json
// panel/web/package.json
{
  "name": "cfvpn-panel-web",
  "private": true,
  "type": "module",
  "scripts": {
    "dev": "vite",
    "build": "vite build",
    "preview": "vite preview",
    "test": "vitest",
    "test:run": "vitest run",
    "e2e": "playwright test"
  },
  "dependencies": {
    "react": "^19.0.0",
    "react-dom": "^19.0.0",
    "react-router-dom": "^7.0.0"
  },
  "devDependencies": {
    "@testing-library/jest-dom": "^6.6.0",
    "@testing-library/react": "^16.2.0",
    "@types/react": "^19.0.0",
    "@types/react-dom": "^19.0.0",
    "@vitejs/plugin-react": "^4.3.0",
    "autoprefixer": "^10.4.20",
    "postcss": "^8.5.0",
    "tailwindcss": "^3.4.16",
    "typescript": "^5.7.0",
    "vite": "^6.0.0",
    "vitest": "^2.1.8"
  }
}
```

- [ ] **Step 4: Create minimal shell implementation**

```tsx
// panel/web/src/app/App.tsx
import { Routes, Route, useNavigate } from 'react-router-dom'

function Home() {
  return <h1 className="text-xl font-semibold">Command Center</h1>
}

export function App() {
  const nav = useNavigate()
  return (
    <div className="min-h-screen bg-slate-950 text-slate-100">
      <header className="border-b border-slate-800 p-3 flex gap-2">
        <button onClick={() => nav('/')}>Command Center</button>
        <button onClick={() => nav('/users')}>Users</button>
        <button onClick={() => nav('/events')}>Events</button>
      </header>
      <main className="p-4">
        <Routes>
          <Route path="/" element={<Home />} />
          <Route path="/users" element={<h1>Users</h1>} />
          <Route path="/events" element={<h1>Events</h1>} />
        </Routes>
      </main>
    </div>
  )
}
```

- [ ] **Step 5: Run test to verify pass**

Run: `npm --prefix panel/web test -- --run App.test.tsx`
Expected: PASS.

- [ ] **Step 6: Commit bootstrap task**

```bash
git add panel/web
git commit -m "feat(panel): bootstrap React app shell with core routes"
```

---

### Task 2: Implement NOC design tokens and status strip

**Files:**
- Create: `panel/web/src/lib/types.ts`, `panel/web/src/components/status/StatusStrip.tsx`
- Modify: `panel/web/src/styles/tailwind.css`, `panel/web/src/pages/CommandCenterPage.tsx`
- Test: `panel/web/src/components/status/StatusStrip.test.tsx`

- [ ] **Step 1: Write failing status-strip test**

```tsx
// panel/web/src/components/status/StatusStrip.test.tsx
import { render, screen } from '@testing-library/react'
import { StatusStrip } from './StatusStrip'

test('renders four KPI tiles and emits filter callback', async () => {
  const onFilter = vi.fn()
  render(
    <StatusStrip
      active={3}
      degraded={1}
      down={0}
      avgLatencyMs={182}
      onFilter={onFilter}
    />,
  )

  await userEvent.click(screen.getByRole('button', { name: /degraded/i }))
  expect(onFilter).toHaveBeenCalledWith('degraded')
  expect(screen.getByText(/182 ms/i)).toBeInTheDocument()
})
```

- [ ] **Step 2: Run test to verify failure**

Run: `npm --prefix panel/web test -- --run StatusStrip.test.tsx`
Expected: FAIL (`StatusStrip` not found).

- [ ] **Step 3: Add NOC token baseline**

```css
/* panel/web/src/styles/tailwind.css */
@tailwind base;
@tailwind components;
@tailwind utilities;

:root {
  --bg: #020617;
  --panel: #0f172a;
  --ok: #22c55e;
  --warn: #f59e0b;
  --down: #ef4444;
  --unknown: #64748b;
}
```

- [ ] **Step 4: Implement StatusStrip component**

```tsx
// panel/web/src/components/status/StatusStrip.tsx
export type NodeFilter = 'all' | 'active' | 'degraded' | 'down'

type Props = {
  active: number
  degraded: number
  down: number
  avgLatencyMs: number
  onFilter: (value: NodeFilter) => void
}

export function StatusStrip({ active, degraded, down, avgLatencyMs, onFilter }: Props) {
  return (
    <section className="grid grid-cols-2 md:grid-cols-4 gap-3">
      <button onClick={() => onFilter('active')} className="rounded-lg bg-slate-900 p-3 text-left">
        <p className="text-xs text-slate-400">Active</p>
        <p className="text-2xl text-green-400 font-semibold">{active}</p>
      </button>
      <button onClick={() => onFilter('degraded')} className="rounded-lg bg-slate-900 p-3 text-left">
        <p className="text-xs text-slate-400">Degraded</p>
        <p className="text-2xl text-amber-400 font-semibold">{degraded}</p>
      </button>
      <button onClick={() => onFilter('down')} className="rounded-lg bg-slate-900 p-3 text-left">
        <p className="text-xs text-slate-400">Down</p>
        <p className="text-2xl text-red-400 font-semibold">{down}</p>
      </button>
      <div className="rounded-lg bg-slate-900 p-3">
        <p className="text-xs text-slate-400">Avg latency</p>
        <p className="text-2xl text-slate-100 font-semibold">{avgLatencyMs} ms</p>
      </div>
    </section>
  )
}
```

- [ ] **Step 5: Integrate strip into command center page**

```tsx
// panel/web/src/pages/CommandCenterPage.tsx (minimal integration)
const [filter, setFilter] = useState<NodeFilter>('all')

return (
  <div className="space-y-4">
    <StatusStrip
      active={activeCount}
      degraded={degradedCount}
      down={downCount}
      avgLatencyMs={avgLatency}
      onFilter={setFilter}
    />
    <NodeGrid nodes={filteredNodes(filter)} />
  </div>
)
```

- [ ] **Step 6: Run tests**

Run: `npm --prefix panel/web test -- --run StatusStrip.test.tsx`
Expected: PASS.

- [ ] **Step 7: Commit task**

```bash
git add panel/web/src/styles/tailwind.css panel/web/src/components/status panel/web/src/pages/CommandCenterPage.tsx
git commit -m "feat(panel): add NOC status strip with KPI filters"
```

---

### Task 3: Build command-center node card grid (desktop-first)

**Files:**
- Create: `panel/web/src/components/nodes/NodeCard.tsx`, `panel/web/src/components/nodes/NodeGrid.tsx`
- Modify: `panel/web/src/pages/CommandCenterPage.tsx`, `panel/web/src/lib/types.ts`
- Test: `panel/web/src/components/nodes/NodeCard.test.tsx`

- [ ] **Step 1: Write failing NodeCard test**

```tsx
// panel/web/src/components/nodes/NodeCard.test.tsx
import { render, screen } from '@testing-library/react'
import { NodeCard } from './NodeCard'

const node = {
  id: 'SG',
  label: 'Singapore',
  status: 'active',
  latencyMs: 95,
  vpnHost: 'b4d82e1a.dongnat247.com',
  lastSeenAt: 1710000000000,
}

test('shows status, latency, host and rotate action', () => {
  render(<NodeCard node={node} onRotate={vi.fn()} onOpen={vi.fn()} onHealthcheck={vi.fn()} />)

  expect(screen.getByText(/singapore/i)).toBeInTheDocument()
  expect(screen.getByText(/95 ms/i)).toBeInTheDocument()
  expect(screen.getByRole('button', { name: /rotate/i })).toBeInTheDocument()
  expect(screen.getByText(/b4d82e1a\.dongnat247\.com/i)).toBeInTheDocument()
})
```

- [ ] **Step 2: Run test and confirm failure**

Run: `npm --prefix panel/web test -- --run NodeCard.test.tsx`
Expected: FAIL.

- [ ] **Step 3: Implement typed node model and card**

```ts
// panel/web/src/lib/types.ts
export type NodeStatus = 'active' | 'degraded' | 'down' | 'unknown'

export type Node = {
  id: string
  label: string
  status: NodeStatus
  latencyMs: number | null
  vpnHost: string
  lastSeenAt: number | null
}
```

```tsx
// panel/web/src/components/nodes/NodeCard.tsx
import type { Node } from '../../lib/types'

type Props = {
  node: Node
  onRotate: (id: string) => void
  onHealthcheck: (id: string) => void
  onOpen: (id: string) => void
}

export function NodeCard({ node, onRotate, onHealthcheck, onOpen }: Props) {
  return (
    <article className="rounded-xl border border-slate-800 bg-slate-900 p-4 space-y-3">
      <div className="flex items-center justify-between">
        <h3 className="font-semibold">{node.label}</h3>
        <span className="text-xs uppercase text-slate-300">{node.status}</span>
      </div>
      <p className="text-sm text-slate-300">{node.latencyMs == null ? 'N/A' : `${node.latencyMs} ms`}</p>
      <p className="text-xs text-slate-400 break-all">{node.vpnHost}</p>
      <div className="flex gap-2">
        <button onClick={() => onRotate(node.id)} className="px-3 py-1 rounded bg-indigo-500 text-white">Rotate</button>
        <button onClick={() => onHealthcheck(node.id)} className="px-3 py-1 rounded bg-slate-800">Healthcheck</button>
        <button onClick={() => onOpen(node.id)} className="px-3 py-1 rounded bg-slate-800">Open</button>
      </div>
    </article>
  )
}
```

- [ ] **Step 4: Add responsive grid wrapper**

```tsx
// panel/web/src/components/nodes/NodeGrid.tsx
import type { Node } from '../../lib/types'
import { NodeCard } from './NodeCard'

type Props = {
  nodes: Node[]
  onRotate: (id: string) => void
  onHealthcheck: (id: string) => void
  onOpen: (id: string) => void
}

export function NodeGrid({ nodes, onRotate, onHealthcheck, onOpen }: Props) {
  return (
    <section className="grid grid-cols-1 md:grid-cols-2 xl:grid-cols-3 gap-4">
      {nodes.map((node) => (
        <NodeCard key={node.id} node={node} onRotate={onRotate} onHealthcheck={onHealthcheck} onOpen={onOpen} />
      ))}
    </section>
  )
}
```

- [ ] **Step 5: Run focused tests**

Run: `npm --prefix panel/web test -- --run NodeCard.test.tsx`
Expected: PASS.

- [ ] **Step 6: Commit task**

```bash
git add panel/web/src/lib/types.ts panel/web/src/components/nodes panel/web/src/pages/CommandCenterPage.tsx
git commit -m "feat(panel): add command-center node cards and responsive grid"
```

---

### Task 4: Add Quick User Panel, QR modal, and mobile bottom sheet

**Files:**
- Create: `panel/web/src/components/users/QuickUserPanel.tsx`, `panel/web/src/components/users/UserBottomSheet.tsx`, `panel/web/src/components/users/QrModal.tsx`
- Modify: `panel/web/src/pages/CommandCenterPage.tsx`
- Test: `panel/web/src/components/users/QuickUserPanel.test.tsx`

- [ ] **Step 1: Write failing quick-user-panel test**

```tsx
// panel/web/src/components/users/QuickUserPanel.test.tsx
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { QuickUserPanel } from './QuickUserPanel'

test('searches user and exposes copy + qr actions', async () => {
  const onCopy = vi.fn()
  const onQr = vi.fn()

  render(
    <QuickUserPanel
      users={[{ id: 'kulinh', name: 'kulinh', nodes: ['SG', 'JP1'] }]}
      onCopy={onCopy}
      onShowQr={onQr}
    />,
  )

  await userEvent.type(screen.getByPlaceholderText(/search user/i), 'ku')
  await userEvent.click(screen.getByRole('button', { name: /copy subscription/i }))

  expect(onCopy).toHaveBeenCalledWith('kulinh')
  expect(screen.getByRole('button', { name: /show qr/i })).toBeInTheDocument()
})
```

- [ ] **Step 2: Run test to verify failure**

Run: `npm --prefix panel/web test -- --run QuickUserPanel.test.tsx`
Expected: FAIL.

- [ ] **Step 3: Implement desktop panel and QR modal**

```tsx
// panel/web/src/components/users/QuickUserPanel.tsx
export function QuickUserPanel({ users, onCopy, onShowQr }: Props) {
  const [q, setQ] = useState('')
  const list = users.filter((u) => u.name.toLowerCase().includes(q.toLowerCase()))

  return (
    <aside className="hidden lg:block w-80 shrink-0 rounded-xl border border-slate-800 bg-slate-900 p-4">
      <input
        placeholder="Search user"
        value={q}
        onChange={(e) => setQ(e.target.value)}
        className="w-full rounded bg-slate-800 p-2"
      />
      <ul className="mt-3 space-y-2">
        {list.map((u) => (
          <li key={u.id} className="rounded bg-slate-800 p-3">
            <p className="font-medium">{u.name}</p>
            <div className="mt-2 flex gap-2">
              <button onClick={() => onCopy(u.id)}>Copy subscription</button>
              <button onClick={() => onShowQr(u.id)}>Show QR</button>
            </div>
          </li>
        ))}
      </ul>
    </aside>
  )
}
```

- [ ] **Step 4: Implement mobile bottom sheet trigger**

```tsx
// panel/web/src/components/users/UserBottomSheet.tsx
export function UserBottomSheet({ open, onClose, children }: Props) {
  if (!open) return null
  return (
    <div className="lg:hidden fixed inset-0 z-50 bg-black/40" onClick={onClose}>
      <div className="absolute bottom-0 left-0 right-0 rounded-t-2xl bg-slate-900 p-4" onClick={(e) => e.stopPropagation()}>
        {children}
      </div>
    </div>
  )
}
```

- [ ] **Step 5: Run focused test**

Run: `npm --prefix panel/web test -- --run QuickUserPanel.test.tsx`
Expected: PASS.

- [ ] **Step 6: Commit task**

```bash
git add panel/web/src/components/users panel/web/src/pages/CommandCenterPage.tsx
git commit -m "feat(panel): add quick user panel and mobile user bottom sheet"
```

---

### Task 5: Implement rotate flow UX states and feedback

**Files:**
- Create: `panel/web/src/components/ui/Toast.tsx`, `panel/web/src/components/ui/ConfirmDialog.tsx`
- Modify: `panel/web/src/lib/api.ts`, `panel/web/src/pages/CommandCenterPage.tsx`, `panel/web/src/components/nodes/NodeCard.tsx`
- Test: `panel/web/src/pages/CommandCenterPage.test.tsx`

- [ ] **Step 1: Write failing rotate-flow test**

```tsx
// panel/web/src/pages/CommandCenterPage.test.tsx
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { CommandCenterPage } from './CommandCenterPage'
import * as api from '../lib/api'

test('rotate node shows loading then success toast', async () => {
  vi.spyOn(api, 'rotateNode').mockResolvedValue({ vpnHost: 'new-host.example.com' })

  render(<CommandCenterPage />)
  await userEvent.click(screen.getAllByRole('button', { name: /rotate/i })[0])
  await userEvent.click(screen.getByRole('button', { name: /confirm rotate/i }))

  expect(await screen.findByText(/rotated successfully/i)).toBeInTheDocument()
})
```

- [ ] **Step 2: Run test to verify failure**

Run: `npm --prefix panel/web test -- --run CommandCenterPage.test.tsx`
Expected: FAIL.

- [ ] **Step 3: Implement rotate API adapter and UI states**

```ts
// panel/web/src/lib/api.ts
export async function rotateNode(nodeId: string): Promise<{ vpnHost: string }> {
  const res = await fetch(`/api/nodes/${nodeId}/rotate`, { method: 'POST' })
  if (!res.ok) throw new Error('rotate failed')
  return res.json()
}
```

```tsx
// panel/web/src/pages/CommandCenterPage.tsx (core state)
const [rotating, setRotating] = useState<string | null>(null)
const [toast, setToast] = useState<string | null>(null)

async function handleRotate(nodeId: string) {
  setRotating(nodeId)
  try {
    const res = await rotateNode(nodeId)
    setNodes((prev) => prev.map((n) => (n.id === nodeId ? { ...n, vpnHost: res.vpnHost } : n)))
    setToast('Rotated successfully')
  } catch {
    setToast('Rotate failed')
  } finally {
    setRotating(null)
  }
}
```

- [ ] **Step 4: Disable rotate while in-progress**

```tsx
// panel/web/src/components/nodes/NodeCard.tsx (button)
<button
  disabled={rotating}
  onClick={() => onRotate(node.id)}
  className="px-3 py-1 rounded bg-indigo-500 disabled:opacity-50"
>
  {rotating ? 'Rotating…' : 'Rotate'}
</button>
```

- [ ] **Step 5: Run test and then full unit suite**

Run: `npm --prefix panel/web test -- --run CommandCenterPage.test.tsx`
Expected: PASS.

Run: `npm --prefix panel/web test -- --run`
Expected: PASS.

- [ ] **Step 6: Commit task**

```bash
git add panel/web/src/lib/api.ts panel/web/src/pages/CommandCenterPage.tsx panel/web/src/components/nodes/NodeCard.tsx panel/web/src/components/ui
git commit -m "feat(panel): add rotate confirmation and action feedback states"
```

---

### Task 6: Implement Users and Events pages with preserved API contracts

**Files:**
- Create: `panel/web/src/pages/UsersPage.tsx`, `panel/web/src/pages/EventsPage.tsx`
- Modify: `panel/web/src/lib/api.ts`, `panel/web/src/app/App.tsx`
- Test: `panel/web/src/pages/UsersPage.test.tsx`, `panel/web/src/pages/EventsPage.test.tsx`

- [ ] **Step 1: Write failing Users page test**

```tsx
// panel/web/src/pages/UsersPage.test.tsx
import { render, screen } from '@testing-library/react'
import { UsersPage } from './UsersPage'
import * as api from '../lib/api'

test('renders users with copy and qr actions', async () => {
  vi.spyOn(api, 'listUsers').mockResolvedValue([{ id: 'kulinh', name: 'kulinh', nodes: ['HK', 'JP1'] }])
  render(<UsersPage />)
  expect(await screen.findByText('kulinh')).toBeInTheDocument()
  expect(screen.getByRole('button', { name: /copy subscription/i })).toBeInTheDocument()
})
```

- [ ] **Step 2: Write failing Events page test**

```tsx
// panel/web/src/pages/EventsPage.test.tsx
import { render, screen } from '@testing-library/react'
import { EventsPage } from './EventsPage'
import * as api from '../lib/api'

test('renders latest event rows', async () => {
  vi.spyOn(api, 'listEvents').mockResolvedValue([{ id: 1, action: 'node.rotate', actor: 'ops@x.com', outcome: 'ok', ts: 1710000000000 }])
  render(<EventsPage />)
  expect(await screen.findByText(/node\.rotate/i)).toBeInTheDocument()
})
```

- [ ] **Step 3: Run tests to verify failures**

Run: `npm --prefix panel/web test -- --run UsersPage.test.tsx EventsPage.test.tsx`
Expected: FAIL.

- [ ] **Step 4: Implement API adapters and pages**

```ts
// panel/web/src/lib/api.ts
export async function listUsers() {
  const res = await fetch('/api/users')
  if (!res.ok) throw new Error('users failed')
  return res.json()
}

export async function listEvents() {
  const res = await fetch('/api/events?limit=200')
  if (!res.ok) throw new Error('events failed')
  return res.json()
}
```

```tsx
// panel/web/src/pages/UsersPage.tsx (core)
export function UsersPage() {
  const [users, setUsers] = useState<UserListItem[]>([])
  useEffect(() => { void listUsers().then(setUsers) }, [])
  return (
    <section className="space-y-3">
      <h1 className="text-xl font-semibold">Users</h1>
      {users.map((u) => (
        <article key={u.id} className="rounded bg-slate-900 p-3">
          <p>{u.name}</p>
          <button>Copy subscription</button>
          <button>Show QR</button>
        </article>
      ))}
    </section>
  )
}
```

- [ ] **Step 5: Wire routes and run tests**

Run: `npm --prefix panel/web test -- --run UsersPage.test.tsx EventsPage.test.tsx`
Expected: PASS.

- [ ] **Step 6: Commit task**

```bash
git add panel/web/src/pages/UsersPage.tsx panel/web/src/pages/EventsPage.tsx panel/web/src/lib/api.ts panel/web/src/app/App.tsx panel/web/src/pages/*.test.tsx
git commit -m "feat(panel): add users and events pages on new navigation model"
```

---

### Task 7: Add responsive acceptance tests and docs updates

**Files:**
- Create: `panel/web/playwright.config.ts`, `panel/web/e2e/command-center.spec.ts`
- Modify: `README.md`, `docs/TESTING.md`

- [ ] **Step 1: Write failing Playwright smoke test**

```ts
// panel/web/e2e/command-center.spec.ts
import { test, expect } from '@playwright/test'

test('desktop command center shows status strip and node cards', async ({ page }) => {
  await page.goto('/')
  await expect(page.getByText('Command Center')).toBeVisible()
  await expect(page.getByText('Active')).toBeVisible()
})

test('mobile opens user bottom sheet from FAB', async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 })
  await page.goto('/')
  await page.getByRole('button', { name: /users/i }).click()
  await expect(page.getByPlaceholder('Search user')).toBeVisible()
})
```

- [ ] **Step 2: Run to verify failure**

Run: `npm --prefix panel/web e2e`
Expected: FAIL until test config/dev server hooks are present.

- [ ] **Step 3: Add Playwright config and pass smoke tests**

```ts
// panel/web/playwright.config.ts
import { defineConfig } from '@playwright/test'

export default defineConfig({
  use: { baseURL: 'http://127.0.0.1:4173' },
  webServer: {
    command: 'npm run preview -- --host 127.0.0.1 --port 4173',
    port: 4173,
    reuseExistingServer: true,
  },
})
```

- [ ] **Step 4: Document frontend workflows**

Add to `README.md`:

```md
## Control Panel Frontend

```bash
npm --prefix panel/web install
npm --prefix panel/web dev
npm --prefix panel/web test -- --run
npm --prefix panel/web e2e
```
```

Add to `docs/TESTING.md` a “UI Redesign Smoke” section using the 4 accepted manual scenarios.

- [ ] **Step 5: Run full verification**

Run:
- `npm --prefix panel/web build`
- `npm --prefix panel/web test -- --run`
- `npm --prefix panel/web e2e`

Expected: all PASS.

- [ ] **Step 6: Commit task**

```bash
git add panel/web README.md docs/TESTING.md
git commit -m "test(panel): add responsive smoke coverage and UI workflow docs"
```

---

## Plan Self-Review

- **Spec coverage:**
  - IA/nav simplification → Tasks 1, 6
  - Command Center with status strip + node cards + quick user panel → Tasks 2, 3, 4
  - Rotate flow UX states → Task 5
  - Desktop/mobile parity → Tasks 3, 4, 7
  - Acceptance + manual validation scenarios → Task 7
- **Placeholder scan:** No TODO/TBD placeholders remain.
- **Type consistency:** `Node`, status filters, and rotate callbacks stay consistent across tasks.

---

Plan complete and saved to `docs/superpowers/plans/2026-04-22-cfvpn-control-panel-ui-redesign-implementation.md`. Two execution options:

1. Subagent-Driven (recommended) - I dispatch a fresh subagent per task, review between tasks, fast iteration

2. Inline Execution - Execute tasks in this session using executing-plans, batch execution with checkpoints

Which approach?