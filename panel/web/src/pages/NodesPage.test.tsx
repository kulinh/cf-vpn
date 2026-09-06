import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import { NodesPage } from './NodesPage'
import * as api from '../lib/api'
import type { Node } from '../lib/types'

function makeNode(overrides: Partial<Node> = {}): Node {
  return {
    id: 'sg',
    label: 'Singapore',
    status: 'active',
    latencyMs: 82,
    vpnHost: 'sg.example.com',
    adminHost: 'sg-admin.example.com',
    lastSeenAt: Date.now(),
    zone: 'example.com',
    createdAt: 0,
    ...overrides,
  }
}

test('renders nodes from API with all action buttons', async () => {
  vi.spyOn(api, 'listNodes').mockResolvedValue([
    makeNode({ id: 'sg', label: 'Singapore', status: 'active', latencyMs: 82 }),
    makeNode({ id: 'jp', label: 'Tokyo', status: 'degraded', latencyMs: 182 }),
    makeNode({ id: 'hk', label: 'Hong Kong', status: 'down', latencyMs: null }),
  ])

  render(<NodesPage />)

  expect(await screen.findByText('Singapore')).toBeInTheDocument()
  expect(screen.getByText('Tokyo')).toBeInTheDocument()
  expect(screen.getByText('Hong Kong')).toBeInTheDocument()
  expect(screen.getByText('active')).toBeInTheDocument()
  expect(screen.getByText('degraded')).toBeInTheDocument()
  expect(screen.getByText('down')).toBeInTheDocument()
  expect(screen.getByRole('button', { name: /check all/i })).toBeInTheDocument()
  expect(screen.getAllByRole('button', { name: /rotate/i })).toHaveLength(3)
  expect(screen.getAllByRole('button', { name: /^check$/i })).toHaveLength(3)
  expect(screen.getAllByRole('button', { name: /edit/i })).toHaveLength(3)
  expect(screen.getAllByRole('button', { name: /delete/i })).toHaveLength(3)
})

test('shows no nodes found when list is empty', async () => {
  vi.spyOn(api, 'listNodes').mockResolvedValue([])

  render(<NodesPage />)

  expect(await screen.findByText(/no nodes found/i)).toBeInTheDocument()
})

test('shows a load-failure banner instead of a silent empty state when listNodes rejects', async () => {
  vi.spyOn(api, 'listNodes').mockRejectedValue(new Error('nodes failed'))

  render(<NodesPage />)

  expect(await screen.findByText(/failed to load — nodes failed\. reload\./i)).toBeInTheDocument()
})

test('rotate shows loading then success toast', async () => {
  vi.spyOn(api, 'listNodes').mockResolvedValue([makeNode({ id: 'sg' })])
  const deferred: { resolve?: (value: api.RotateNodeResponse) => void } = {}
  vi.spyOn(api, 'rotateNode').mockImplementation(
    () =>
      new Promise((resolve) => {
        deferred.resolve = resolve
      }),
  )

  render(<NodesPage />)
  await screen.findByText('Singapore')

  fireEvent.click(screen.getByRole('button', { name: /rotate/i }))
  fireEvent.click(screen.getByRole('button', { name: /confirm rotate/i }))

  expect(screen.getByRole('button', { name: /rotating/i })).toBeDisabled()

  deferred.resolve?.({ vpnHost: 'new-host.example.com' })

  expect(await screen.findByText(/rotated successfully/i)).toBeInTheDocument()
  expect(screen.getByText(/new-host\.example\.com/i)).toBeInTheDocument()
})

test('shows the thrown detail in the toast and refetches nodes when rotate fails', async () => {
  const listNodesSpy = vi
    .spyOn(api, 'listNodes')
    .mockResolvedValueOnce([makeNode({ id: 'sg', vpnHost: 'old-vpn.example.com' })])
    .mockResolvedValueOnce([makeNode({ id: 'sg', vpnHost: 'agent-actual-host.example.com' })])
  vi.spyOn(api, 'rotateNode').mockRejectedValue(new Error('do NOT retry'))

  render(<NodesPage />)
  await screen.findByText('Singapore')

  fireEvent.click(screen.getByRole('button', { name: /rotate/i }))
  fireEvent.click(screen.getByRole('button', { name: /confirm rotate/i }))

  expect(await screen.findByText(/do NOT retry/)).toBeInTheDocument()
  expect(await screen.findByText(/agent-actual-host\.example\.com/i)).toBeInTheDocument()
  expect(listNodesSpy).toHaveBeenCalledTimes(2)
})

test('rotate updates both vpn host and hy2 host in the row', async () => {
  vi.spyOn(api, 'listNodes').mockResolvedValue([
    makeNode({ id: 'sg', vpnHost: 'old-vpn.example.com', hy2Host: 'old-hy2.example.com', hy2Port: 443 }),
  ])
  vi.spyOn(api, 'rotateNode').mockResolvedValue({
    vpnHost: 'new-vpn.example.com',
    hy2Host: 'new-hy2.example.com',
    hy2Port: 8443,
    publicIp: '203.0.113.10',
  })

  render(<NodesPage />)
  await screen.findByText('Singapore')

  fireEvent.click(screen.getByRole('button', { name: /rotate/i }))
  fireEvent.click(screen.getByRole('button', { name: /confirm rotate/i }))

  expect(await screen.findByText(/rotated successfully/i)).toBeInTheDocument()
  expect(screen.getByText(/new-vpn\.example\.com/i)).toBeInTheDocument()
  expect(screen.getByText(/new-hy2\.example\.com/i)).toBeInTheDocument()
  expect(screen.queryByText(/old-hy2\.example\.com/i)).not.toBeInTheDocument()
})

test('delete confirms then removes the node from the table', async () => {
  vi.spyOn(api, 'listNodes').mockResolvedValue([
    makeNode({ id: 'sg', label: 'Singapore' }),
    makeNode({ id: 'jp', label: 'Tokyo' }),
  ])
  const deleteSpy = vi.spyOn(api, 'deleteNode').mockResolvedValue({ warnings: [] })

  render(<NodesPage />)
  await screen.findByText('Singapore')

  const deleteButtons = screen.getAllByRole('button', { name: /^delete$/i })
  fireEvent.click(deleteButtons[0])

  fireEvent.click(screen.getByRole('button', { name: /confirm delete/i }))

  expect(await screen.findByText(/node deleted/i)).toBeInTheDocument()
  expect(deleteSpy).toHaveBeenCalledWith('sg')
  expect(screen.queryByText('Singapore')).not.toBeInTheDocument()
  expect(screen.getByText('Tokyo')).toBeInTheDocument()
})

test('delete failure shows error toast and keeps node', async () => {
  vi.spyOn(api, 'listNodes').mockResolvedValue([makeNode({ id: 'sg', label: 'Singapore' })])
  vi.spyOn(api, 'deleteNode').mockRejectedValue(new Error('boom'))

  render(<NodesPage />)
  await screen.findByText('Singapore')

  fireEvent.click(screen.getByRole('button', { name: /^delete$/i }))
  fireEvent.click(screen.getByRole('button', { name: /confirm delete/i }))

  expect(await screen.findByText(/delete failed/i)).toBeInTheDocument()
  expect(screen.getByText('Singapore')).toBeInTheDocument()
})

test('check-all results follow node id, not array position, when a node is deleted mid-batch (M-R2)', async () => {
  vi.spyOn(api, 'listNodes').mockResolvedValue([
    makeNode({ id: 'sg', label: 'Singapore' }),
    makeNode({ id: 'jp', label: 'Tokyo' }),
    makeNode({ id: 'hk', label: 'Hong Kong' }),
  ])

  const pending: Record<string, { resolve: (v: { latency_ms: number }) => void; reject: (e: Error) => void }> = {}
  vi.spyOn(api, 'healthcheckNode').mockImplementation(
    (nodeId) =>
      new Promise((resolve, reject) => {
        pending[nodeId] = { resolve, reject }
      }),
  )
  vi.spyOn(api, 'deleteNode').mockResolvedValue({ warnings: [] })

  render(<NodesPage />)
  await screen.findByText('Singapore')

  fireEvent.click(screen.getByRole('button', { name: /check all/i }))
  await waitFor(() => expect(Object.keys(pending)).toEqual(['sg', 'jp', 'hk']))

  // Delete Singapore (the first row) while its own healthcheck — and the
  // other two — are still in flight. This shrinks the `nodes` array that
  // the batch resolution zips against.
  fireEvent.click(screen.getAllByRole('button', { name: /^delete$/i })[0])
  fireEvent.click(screen.getByRole('button', { name: /confirm delete/i }))
  await waitFor(() => expect(screen.queryByText('Singapore')).not.toBeInTheDocument())

  // Resolve out of row order, with a value for `sg` that would leak into
  // Tokyo's cell under index-based zipping (sg was array position 0, and
  // after the delete Tokyo becomes position 0).
  pending.sg.resolve({ latency_ms: 999 })
  pending.jp.resolve({ latency_ms: 55 })
  pending.hk.reject(new Error('unreachable'))

  await screen.findByText(/checked 3 nodes/i)

  expect(screen.getByText('55 ms')).toBeInTheDocument()
  expect(screen.queryByText('999 ms')).not.toBeInTheDocument()
  expect(screen.getByText('unreachable')).toBeInTheDocument()
})

test('checks all node latency and marks failed nodes unreachable', async () => {
  vi.spyOn(api, 'listNodes').mockResolvedValue([
    makeNode({ id: 'sg', label: 'Singapore', latencyMs: null }),
    makeNode({ id: 'jp', label: 'Tokyo', latencyMs: null }),
  ])
  vi.spyOn(api, 'healthcheckNode').mockImplementation(async (nodeId) => {
    if (nodeId === 'sg') return { latency_ms: 91 }
    throw new Error('boom')
  })

  render(<NodesPage />)
  await screen.findByText('Singapore')

  fireEvent.click(screen.getByRole('button', { name: /check all/i }))

  expect(await screen.findByText(/checked 2 nodes, 1 alive/i)).toBeInTheDocument()
  expect(screen.getByText('91 ms')).toBeInTheDocument()
  expect(screen.getByText('unreachable')).toBeInTheDocument()
})
