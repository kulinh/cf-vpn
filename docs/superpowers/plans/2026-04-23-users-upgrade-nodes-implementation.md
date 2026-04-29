# Users Upgrade Nodes Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a per-user Upgrade action that calls a new backend endpoint to add only missing node assignments, with confirm UX and mobile-safe states on the Users page.

**Architecture:** Implement a dedicated worker endpoint `POST /api/users/:id/upgrade-nodes` with idempotent missing-node sync logic, then wire the frontend to compute per-user missing-node counts from `listNodes()` and execute per-user upgrades via confirm dialog. Keep business logic in a small pure function (`upgradeUserNodes`) so backend behavior is easy to unit test without D1 internals in every test. Reuse existing `ConfirmDialog` and `Toast` components for consistent UX.

**Tech Stack:** Cloudflare Worker (TypeScript + D1), React + TypeScript (Vite), Vitest + Testing Library.

---

## Scope Check

This is one subsystem (user-node sync) spanning API + UI for a single feature and can be delivered in one plan.

---

## File Structure

**Create:**
- `panel/worker/package.json` — worker package scripts and test dependencies
- `panel/worker/tsconfig.json` — worker TypeScript config
- `panel/worker/vitest.config.ts` — worker test config
- `panel/worker/src/lib/upgradeUserNodes.ts` — pure sync logic and result contract
- `panel/worker/src/lib/upgradeUserNodes.test.ts` — pure logic tests (add-only missing, no-op, not-found)
- `panel/worker/src/lib/d1UserNodesRepo.ts` — D1-backed repo implementation for upgrade flow
- `panel/worker/src/index.ts` — worker route wiring for `POST /api/users/:id/upgrade-nodes`
- `panel/worker/src/index.test.ts` — HTTP contract tests for the new endpoint
- `panel/web/src/lib/api.test.ts` — API client parsing and endpoint contract tests

**Modify:**
- `panel/web/src/lib/types.ts` — add `UpgradeUserNodesResponse` type
- `panel/web/src/lib/api.ts` — add `upgradeUserNodes(userId)` client method
- `panel/web/src/components/ui/ConfirmDialog.tsx` — make in-progress confirm label configurable
- `panel/web/src/pages/UsersPage.tsx` — add Upgrade button, confirm flow, loading lock, toast, local state update
- `panel/web/src/pages/UsersPage.test.tsx` — add/adjust tests for upgrade states and keep existing QR/copy behavior passing

---

### Task 1: Scaffold worker package for API tests

**Files:**
- Create: `panel/worker/package.json`, `panel/worker/tsconfig.json`, `panel/worker/vitest.config.ts`

- [ ] **Step 1: Add worker package manifest**

```json
// panel/worker/package.json
{
  "name": "cfvpn-panel-worker",
  "private": true,
  "type": "module",
  "scripts": {
    "test": "vitest",
    "test:run": "vitest run"
  },
  "devDependencies": {
    "@cloudflare/workers-types": "^4.20260412.0",
    "typescript": "^5.7.0",
    "vitest": "^2.1.8"
  }
}
```

- [ ] **Step 2: Add TypeScript and Vitest config**

```json
// panel/worker/tsconfig.json
{
  "compilerOptions": {
    "target": "ES2022",
    "module": "ESNext",
    "moduleResolution": "Bundler",
    "strict": true,
    "types": ["@cloudflare/workers-types", "vitest/globals"],
    "noEmit": true
  },
  "include": ["src/**/*.ts"]
}
```

```ts
// panel/worker/vitest.config.ts
import { defineConfig } from 'vitest/config'

export default defineConfig({
  test: {
    environment: 'node',
    globals: true,
    include: ['src/**/*.test.ts'],
  },
})
```

- [ ] **Step 3: Install worker test dependencies**

Run: `npm --prefix panel/worker install`
Expected: lockfile created under `panel/worker` and install completes without errors.

- [ ] **Step 4: Commit scaffold**

```bash
git add panel/worker/package.json panel/worker/tsconfig.json panel/worker/vitest.config.ts panel/worker/package-lock.json
git commit -m "chore(worker): scaffold test package for API routes"
```

---

### Task 2: Implement pure upgrade logic with TDD

**Files:**
- Create: `panel/worker/src/lib/upgradeUserNodes.ts`, `panel/worker/src/lib/upgradeUserNodes.test.ts`

- [ ] **Step 1: Write failing unit tests for upgrade behavior**

```ts
// panel/worker/src/lib/upgradeUserNodes.test.ts
import { describe, expect, it, vi } from 'vitest'
import { NotFoundError, upgradeUserNodes } from './upgradeUserNodes'

type Repo = {
  userExists: (userId: string) => Promise<boolean>
  listAllNodeIds: () => Promise<string[]>
  listUserNodeIds: (userId: string) => Promise<string[]>
  addUserNode: (userId: string, nodeId: string, createdAt: number) => Promise<void>
}

describe('upgradeUserNodes', () => {
  it('adds only missing nodes and returns counts', async () => {
    const addUserNode = vi.fn(async () => {})
    const repo: Repo = {
      userExists: async () => true,
      listAllNodeIds: async () => ['HK', 'JP1', 'JP2', 'SG', 'VN'],
      listUserNodeIds: async () => ['HK', 'JP1', 'JP2', 'SG'],
      addUserNode,
    }

    const result = await upgradeUserNodes(repo, 'kulinh', 1770000000000)

    expect(addUserNode).toHaveBeenCalledTimes(1)
    expect(addUserNode).toHaveBeenCalledWith('kulinh', 'VN', 1770000000000)
    expect(result).toEqual({
      userId: 'kulinh',
      addedNodes: ['VN'],
      addedCount: 1,
      alreadyPresentCount: 4,
      totalNodesAfterUpgrade: 5,
    })
  })

  it('returns no-op result when user is already up-to-date', async () => {
    const addUserNode = vi.fn(async () => {})
    const repo: Repo = {
      userExists: async () => true,
      listAllNodeIds: async () => ['HK', 'JP1'],
      listUserNodeIds: async () => ['HK', 'JP1'],
      addUserNode,
    }

    const result = await upgradeUserNodes(repo, 'kulinh', 1770000000000)

    expect(addUserNode).not.toHaveBeenCalled()
    expect(result.addedCount).toBe(0)
    expect(result.alreadyPresentCount).toBe(2)
    expect(result.totalNodesAfterUpgrade).toBe(2)
  })

  it('throws NotFoundError when user does not exist', async () => {
    const repo: Repo = {
      userExists: async () => false,
      listAllNodeIds: async () => [],
      listUserNodeIds: async () => [],
      addUserNode: async () => {},
    }

    await expect(upgradeUserNodes(repo, 'missing-user', 1770000000000)).rejects.toBeInstanceOf(NotFoundError)
  })
})
```

- [ ] **Step 2: Run unit test and verify failure**

Run: `npm --prefix panel/worker test -- --run src/lib/upgradeUserNodes.test.ts`
Expected: FAIL with module/file not found for `./upgradeUserNodes`.

- [ ] **Step 3: Implement minimal pure logic**

```ts
// panel/worker/src/lib/upgradeUserNodes.ts
export type UpgradeUserNodesResult = {
  userId: string
  addedNodes: string[]
  addedCount: number
  alreadyPresentCount: number
  totalNodesAfterUpgrade: number
}

export type UpgradeUserNodesRepo = {
  userExists: (userId: string) => Promise<boolean>
  listAllNodeIds: () => Promise<string[]>
  listUserNodeIds: (userId: string) => Promise<string[]>
  addUserNode: (userId: string, nodeId: string, createdAt: number) => Promise<void>
}

export class NotFoundError extends Error {}

function normalizeNodeId(id: string): string {
  return id.trim().toUpperCase()
}

export async function upgradeUserNodes(
  repo: UpgradeUserNodesRepo,
  userId: string,
  nowMs: number,
): Promise<UpgradeUserNodesResult> {
  const exists = await repo.userExists(userId)
  if (!exists) {
    throw new NotFoundError('user not found')
  }

  const allNodeIds = (await repo.listAllNodeIds()).map(normalizeNodeId)
  const userNodeSet = new Set((await repo.listUserNodeIds(userId)).map(normalizeNodeId))

  const addedNodes: string[] = []

  for (const nodeId of allNodeIds) {
    if (userNodeSet.has(nodeId)) {
      continue
    }

    await repo.addUserNode(userId, nodeId, nowMs)
    userNodeSet.add(nodeId)
    addedNodes.push(nodeId)
  }

  return {
    userId,
    addedNodes,
    addedCount: addedNodes.length,
    alreadyPresentCount: allNodeIds.length - addedNodes.length,
    totalNodesAfterUpgrade: userNodeSet.size,
  }
}
```

- [ ] **Step 4: Run unit tests and verify pass**

Run: `npm --prefix panel/worker test -- --run src/lib/upgradeUserNodes.test.ts`
Expected: PASS (3 tests).

- [ ] **Step 5: Commit pure logic task**

```bash
git add panel/worker/src/lib/upgradeUserNodes.ts panel/worker/src/lib/upgradeUserNodes.test.ts
git commit -m "feat(worker): add idempotent user missing-node upgrade logic"
```

---

### Task 3: Wire D1 repository and HTTP endpoint contract

**Files:**
- Create: `panel/worker/src/lib/d1UserNodesRepo.ts`, `panel/worker/src/index.ts`, `panel/worker/src/index.test.ts`

- [ ] **Step 1: Write failing endpoint contract test**

```ts
// panel/worker/src/index.test.ts
import { describe, expect, it } from 'vitest'
import worker from './index'

function makeEnv() {
  const state = {
    users: new Set(['kulinh']),
    nodes: ['HK', 'JP1', 'JP2', 'SG', 'VN'],
    userNodes: new Map<string, Set<string>>([['kulinh', new Set(['HK', 'JP1', 'JP2', 'SG'])]]),
  }

  const DB = {
    prepare(sql: string) {
      return {
        bind(...args: unknown[]) {
          return {
            async first() {
              if (sql.includes('FROM users')) {
                const userId = String(args[0])
                return state.users.has(userId) ? { id: userId } : null
              }
              return null
            },
            async all() {
              if (sql.includes('SELECT id FROM nodes')) {
                return { results: state.nodes.map((id) => ({ id })) }
              }
              if (sql.includes('SELECT node_id FROM user_nodes')) {
                const userId = String(args[0])
                const rows = [...(state.userNodes.get(userId) ?? new Set())].map((node_id) => ({ node_id }))
                return { results: rows }
              }
              return { results: [] }
            },
            async run() {
              if (sql.includes('INSERT INTO user_nodes')) {
                const userId = String(args[0])
                const nodeId = String(args[1])
                const set = state.userNodes.get(userId) ?? new Set<string>()
                set.add(nodeId)
                state.userNodes.set(userId, set)
              }
              return { success: true }
            },
          }
        },
      }
    },
  }

  return { DB }
}

describe('POST /api/users/:id/upgrade-nodes', () => {
  it('returns added nodes and counts', async () => {
    const env = makeEnv()
    const req = new Request('https://example.com/api/users/kulinh/upgrade-nodes', { method: 'POST' })

    const res = await worker.fetch(req, env as never)
    const body = (await res.json()) as {
      userId: string
      addedNodes: string[]
      addedCount: number
      alreadyPresentCount: number
      totalNodesAfterUpgrade: number
    }

    expect(res.status).toBe(200)
    expect(body).toEqual({
      userId: 'kulinh',
      addedNodes: ['VN'],
      addedCount: 1,
      alreadyPresentCount: 4,
      totalNodesAfterUpgrade: 5,
    })
  })

  it('returns 404 for unknown user', async () => {
    const env = makeEnv()
    const req = new Request('https://example.com/api/users/ghost/upgrade-nodes', { method: 'POST' })

    const res = await worker.fetch(req, env as never)

    expect(res.status).toBe(404)
  })
})
```

- [ ] **Step 2: Run endpoint tests and verify failure**

Run: `npm --prefix panel/worker test -- --run src/index.test.ts`
Expected: FAIL because `src/index.ts` does not exist yet.

- [ ] **Step 3: Implement D1 repo adapter**

```ts
// panel/worker/src/lib/d1UserNodesRepo.ts
import type { UpgradeUserNodesRepo } from './upgradeUserNodes'

export function createD1UserNodesRepo(db: D1Database): UpgradeUserNodesRepo {
  return {
    async userExists(userId) {
      const row = await db.prepare('SELECT id FROM users WHERE id = ?').bind(userId).first<{ id: string }>()
      return row != null
    },
    async listAllNodeIds() {
      const rows = await db.prepare('SELECT id FROM nodes').all<{ id: string }>()
      return (rows.results ?? []).map((r) => r.id)
    },
    async listUserNodeIds(userId) {
      const rows = await db
        .prepare('SELECT node_id FROM user_nodes WHERE user_id = ?')
        .bind(userId)
        .all<{ node_id: string }>()
      return (rows.results ?? []).map((r) => r.node_id)
    },
    async addUserNode(userId, nodeId, createdAt) {
      await db
        .prepare('INSERT INTO user_nodes (user_id, node_id, created_at) VALUES (?, ?, ?)')
        .bind(userId, nodeId, createdAt)
        .run()
    },
  }
}
```

- [ ] **Step 4: Implement route handler**

```ts
// panel/worker/src/index.ts
import { createD1UserNodesRepo } from './lib/d1UserNodesRepo'
import { NotFoundError, upgradeUserNodes } from './lib/upgradeUserNodes'

type Env = {
  DB: D1Database
}

function json(data: unknown, status = 200): Response {
  return new Response(JSON.stringify(data), {
    status,
    headers: { 'content-type': 'application/json' },
  })
}

export default {
  async fetch(request: Request, env: Env): Promise<Response> {
    const url = new URL(request.url)
    const match = url.pathname.match(/^\/api\/users\/([^/]+)\/upgrade-nodes$/)

    if (request.method === 'POST' && match) {
      const userId = decodeURIComponent(match[1])

      try {
        const repo = createD1UserNodesRepo(env.DB)
        const result = await upgradeUserNodes(repo, userId, Date.now())
        return json(result)
      } catch (error) {
        if (error instanceof NotFoundError) {
          return json({ error: 'user not found' }, 404)
        }

        return json({ error: 'upgrade failed' }, 500)
      }
    }

    return json({ error: 'not found' }, 404)
  },
}
```

- [ ] **Step 5: Run endpoint tests and verify pass**

Run: `npm --prefix panel/worker test -- --run src/index.test.ts`
Expected: PASS (2 tests).

- [ ] **Step 6: Commit endpoint task**

```bash
git add panel/worker/src/lib/d1UserNodesRepo.ts panel/worker/src/index.ts panel/worker/src/index.test.ts
git commit -m "feat(worker): add users upgrade-nodes endpoint"
```

---

### Task 4: Add frontend API contract and parser with tests

**Files:**
- Modify: `panel/web/src/lib/types.ts`, `panel/web/src/lib/api.ts`
- Create: `panel/web/src/lib/api.test.ts`

- [ ] **Step 1: Write failing API client tests**

```ts
// panel/web/src/lib/api.test.ts
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { upgradeUserNodes } from './api'

describe('upgradeUserNodes', () => {
  beforeEach(() => {
    vi.restoreAllMocks()
  })

  it('calls POST /api/users/:id/upgrade-nodes and returns parsed payload', async () => {
    const fetchMock = vi.spyOn(globalThis, 'fetch' as never).mockResolvedValue({
      ok: true,
      json: async () => ({
        userId: 'kulinh',
        addedNodes: ['VN'],
        addedCount: 1,
        alreadyPresentCount: 4,
        totalNodesAfterUpgrade: 5,
      }),
    } as Response)

    const result = await upgradeUserNodes('kulinh')

    expect(fetchMock).toHaveBeenCalledWith('/api/users/kulinh/upgrade-nodes', { method: 'POST' })
    expect(result.addedNodes).toEqual(['VN'])
    expect(result.addedCount).toBe(1)
  })

  it('throws when response is not ok', async () => {
    vi.spyOn(globalThis, 'fetch' as never).mockResolvedValue({ ok: false } as Response)

    await expect(upgradeUserNodes('kulinh')).rejects.toThrow('upgrade user nodes failed')
  })
})
```

- [ ] **Step 2: Run test and verify failure**

Run: `npm --prefix panel/web test -- --run src/lib/api.test.ts`
Expected: FAIL because `upgradeUserNodes` is missing.

- [ ] **Step 3: Add response type to shared types**

```ts
// panel/web/src/lib/types.ts
export type UpgradeUserNodesResponse = {
  userId: string
  addedNodes: string[]
  addedCount: number
  alreadyPresentCount: number
  totalNodesAfterUpgrade: number
}
```

- [ ] **Step 4: Add API client function**

```ts
// panel/web/src/lib/api.ts
import type { Event, Node, UpgradeUserNodesResponse, User } from './types'

export async function upgradeUserNodes(userId: string): Promise<UpgradeUserNodesResponse> {
  const response = await fetch(`/api/users/${encodeURIComponent(userId)}/upgrade-nodes`, {
    method: 'POST',
  })

  if (!response.ok) {
    throw new Error('upgrade user nodes failed')
  }

  return (await response.json()) as UpgradeUserNodesResponse
}
```

- [ ] **Step 5: Run API tests and verify pass**

Run: `npm --prefix panel/web test -- --run src/lib/api.test.ts`
Expected: PASS (2 tests).

- [ ] **Step 6: Commit API client task**

```bash
git add panel/web/src/lib/types.ts panel/web/src/lib/api.ts panel/web/src/lib/api.test.ts
git commit -m "feat(panel): add upgrade-user-nodes API client"
```

---

### Task 5: Add Users page Upgrade UX (confirm, loading, up-to-date state)

**Files:**
- Modify: `panel/web/src/components/ui/ConfirmDialog.tsx`
- Modify: `panel/web/src/pages/UsersPage.tsx`, `panel/web/src/pages/UsersPage.test.tsx`

- [ ] **Step 1: Add failing UsersPage tests for upgrade UX**

```tsx
// panel/web/src/pages/UsersPage.test.tsx (additions)
import * as api from '../lib/api'

function mockUsersAndNodes() {
  vi.spyOn(api, 'listUsers').mockResolvedValue([
    { id: 'kulinh', name: 'kulinh', nodes: ['HK', 'JP1', 'JP2', 'SG'] },
    { id: 'minh', name: 'minh', nodes: ['HK', 'JP1', 'JP2', 'SG', 'VN'] },
  ])

  vi.spyOn(api, 'listNodes').mockResolvedValue([
    { id: 'HK', label: 'HK', status: 'active', latencyMs: 10, adminHost: '', vpnHost: '', zone: '', lastSeenAt: null },
    { id: 'JP1', label: 'JP1', status: 'active', latencyMs: 10, adminHost: '', vpnHost: '', zone: '', lastSeenAt: null },
    { id: 'JP2', label: 'JP2', status: 'active', latencyMs: 10, adminHost: '', vpnHost: '', zone: '', lastSeenAt: null },
    { id: 'SG', label: 'SG', status: 'active', latencyMs: 10, adminHost: '', vpnHost: '', zone: '', lastSeenAt: null },
    { id: 'VN', label: 'VN', status: 'active', latencyMs: 10, adminHost: '', vpnHost: '', zone: '', lastSeenAt: null },
  ])
}

test('shows Upgrade (+N) and Up-to-date states per user', async () => {
  mockUsersAndNodes()
  vi.spyOn(api, 'getUserSubscription').mockResolvedValue({ text: 'vless://x', qrData: 'dmxlc3M6Ly94' })

  render(<UsersPage />)

  expect(await screen.findByText('kulinh')).toBeInTheDocument()
  expect(screen.getByRole('button', { name: /upgrade \(\+1\)/i })).toBeEnabled()
  expect(screen.getByRole('button', { name: /up-to-date/i })).toBeDisabled()
})

test('confirms and upgrades user with missing node', async () => {
  mockUsersAndNodes()
  vi.spyOn(api, 'getUserSubscription').mockResolvedValue({ text: 'vless://x', qrData: 'dmxlc3M6Ly94' })
  vi.spyOn(api, 'upgradeUserNodes').mockResolvedValue({
    userId: 'kulinh',
    addedNodes: ['VN'],
    addedCount: 1,
    alreadyPresentCount: 4,
    totalNodesAfterUpgrade: 5,
  })

  render(<UsersPage />)

  fireEvent.click(await screen.findByRole('button', { name: /upgrade \(\+1\)/i }))
  expect(screen.getByRole('dialog')).toBeInTheDocument()

  fireEvent.click(screen.getByRole('button', { name: /confirm upgrade/i }))

  await waitFor(() => {
    expect(api.upgradeUserNodes).toHaveBeenCalledWith('kulinh')
  })

  await waitFor(() => {
    expect(screen.getAllByRole('button', { name: /up-to-date/i })[0]).toBeDisabled()
  })
})
```

- [ ] **Step 2: Run UsersPage tests and verify failure**

Run: `npm --prefix panel/web test -- --run src/pages/UsersPage.test.tsx`
Expected: FAIL (missing `listNodes`/`upgradeUserNodes` usage and missing Upgrade controls).

- [ ] **Step 3: Make ConfirmDialog loading label reusable**

```tsx
// panel/web/src/components/ui/ConfirmDialog.tsx
type ConfirmDialogProps = {
  open: boolean
  title: string
  message?: string
  confirmLabel?: string
  cancelLabel?: string
  confirming?: boolean
  confirmingLabel?: string
  onConfirm: () => void
  onCancel: () => void
}

export function ConfirmDialog({
  open,
  title,
  message,
  confirmLabel = 'Confirm',
  cancelLabel = 'Cancel',
  confirming = false,
  confirmingLabel = 'Working...',
  onConfirm,
  onCancel,
}: ConfirmDialogProps) {
  // ...keep existing structure
  // confirm button text:
  // {confirming ? confirmingLabel : confirmLabel}
}
```

- [ ] **Step 4: Implement UsersPage upgrade flow and per-user state**

```tsx
// panel/web/src/pages/UsersPage.tsx (core additions)
import { useEffect, useMemo, useRef, useState } from 'react'
import { ConfirmDialog } from '../components/ui/ConfirmDialog'
import { Toast } from '../components/ui/Toast'
import { getUserSubscription, listNodes, listUsers, upgradeUserNodes } from '../lib/api'

function normalizeNodeId(id: string): string {
  return id.trim().toUpperCase()
}

export function UsersPage() {
  const [users, setUsers] = useState<User[]>([])
  const [allNodeIds, setAllNodeIds] = useState<string[]>([])
  const [upgradeUserId, setUpgradeUserId] = useState<string | null>(null)
  const [upgradingUserId, setUpgradingUserId] = useState<string | null>(null)
  const [toastMessage, setToastMessage] = useState<string | null>(null)
  // keep existing QR state

  useEffect(() => {
    let mounted = true

    void Promise.all([listUsers(), listNodes()]).then(([userItems, nodeItems]) => {
      if (!mounted) {
        return
      }

      setUsers(userItems)
      setAllNodeIds(nodeItems.map((n) => normalizeNodeId(n.id)))
    })

    return () => {
      mounted = false
    }
  }, [])

  const missingCountByUserId = useMemo(() => {
    const allNodeSet = new Set(allNodeIds)
    const map = new Map<string, number>()

    for (const user of users) {
      const existing = new Set(user.nodes.map(normalizeNodeId))
      let missing = 0
      for (const nodeId of allNodeSet) {
        if (!existing.has(nodeId)) {
          missing += 1
        }
      }
      map.set(user.id, missing)
    }

    return map
  }, [allNodeIds, users])

  const handleConfirmUpgrade = async () => {
    if (!upgradeUserId) {
      return
    }

    const userId = upgradeUserId
    setUpgradingUserId(userId)

    try {
      const result = await upgradeUserNodes(userId)
      setUsers((prev) =>
        prev.map((user) => {
          if (user.id !== userId) {
            return user
          }

          const merged = new Set(user.nodes.map(normalizeNodeId))
          for (const nodeId of result.addedNodes) {
            merged.add(normalizeNodeId(nodeId))
          }

          return { ...user, nodes: [...merged] }
        }),
      )

      setToastMessage(result.addedCount > 0 ? `Added ${result.addedCount} node(s)` : 'User is already up-to-date')
      setUpgradeUserId(null)
    } catch {
      setToastMessage('Upgrade failed')
    } finally {
      setUpgradingUserId(null)
    }
  }

  // in each user card action row:
  // const missingCount = missingCountByUserId.get(user.id) ?? 0
  // show button Upgrade (+N) when missingCount > 0 else disabled Up-to-date

  return (
    <>
      {/* existing users rendering + qr modal */}
      <ConfirmDialog
        open={upgradeUserId != null}
        title={`Upgrade user ${upgradeUserId ?? ''}?`}
        message={`Add ${missingCountByUserId.get(upgradeUserId ?? '') ?? 0} missing nodes. Existing nodes are kept.`}
        confirmLabel="Confirm Upgrade"
        confirming={upgradingUserId != null}
        confirmingLabel="Upgrading..."
        onConfirm={() => {
          void handleConfirmUpgrade()
        }}
        onCancel={() => {
          if (upgradingUserId == null) {
            setUpgradeUserId(null)
          }
        }}
      />
      <Toast message={toastMessage} onClose={() => setToastMessage(null)} />
    </>
  )
}
```

- [ ] **Step 5: Run Users page and regression tests**

Run: `npm --prefix panel/web test -- --run src/pages/UsersPage.test.tsx src/pages/CommandCenterPage.test.tsx`
Expected: PASS (new upgrade tests and no regression to confirm dialog usage in command center).

- [ ] **Step 6: Commit UsersPage task**

```bash
git add panel/web/src/components/ui/ConfirmDialog.tsx panel/web/src/pages/UsersPage.tsx panel/web/src/pages/UsersPage.test.tsx
git commit -m "feat(panel): add per-user upgrade-to-missing-nodes UX"
```

---

### Task 6: Final verification and integration commit

**Files:**
- Modify: any snapshots/lockfiles generated by previous steps

- [ ] **Step 1: Run full targeted test suites**

Run: `npm --prefix panel/worker test -- --run`
Expected: PASS for worker unit + route tests.

Run: `npm --prefix panel/web test -- --run src/lib/api.test.ts src/pages/UsersPage.test.tsx`
Expected: PASS for frontend API + Users page upgrade scenarios.

- [ ] **Step 2: Manual smoke in browser (mobile-focused)**

Run: `npm --prefix panel/web dev`
Expected: dev server starts.

Manual checks on `/users`:
- user with missing nodes shows `Upgrade (+N)` button;
- tapping Upgrade opens confirm dialog and confirm button shows `Upgrading...` while pending;
- success updates `Nodes:` text and button becomes `Up-to-date` when fully synced;
- user already fully synced starts as disabled `Up-to-date`;
- failure path shows toast and keeps existing nodes unchanged.

- [ ] **Step 3: Commit verification adjustments (if any)**

```bash
git add panel/worker panel/web
git commit -m "test(panel): verify upgrade-nodes flow and backend contract"
```

---

## Spec Coverage Self-Review

- Endpoint `POST /api/users/{id}/upgrade-nodes`: covered in Task 3.
- Add only missing nodes and skip existing: covered in Task 2 tests and implementation.
- Idempotent no-op result: covered in Task 2 no-op test.
- Users UI states `Upgrade (+N)` and disabled `Up-to-date`: covered in Task 5.
- Confirmation UX and mobile-safe loading behavior: covered in Task 5 + Task 6 manual smoke.
- Frontend API client coverage: covered in Task 4.
- Backend test coverage for not-found and success contract: covered in Task 3.

No placeholders, no unresolved TODOs, and method/type names are consistent across tasks.
