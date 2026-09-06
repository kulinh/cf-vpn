import { render, screen } from '@testing-library/react'
import { CommandCenterPage } from './CommandCenterPage'
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

test('renders fleet overview without VPN host or rotate actions', async () => {
  vi.spyOn(api, 'listNodes').mockResolvedValue([
    makeNode({ id: 'sg', label: 'Singapore', vpnHost: 'sg.example.com', latencyMs: 82 }),
    makeNode({ id: 'jp', label: 'Tokyo', vpnHost: 'jp.example.com', latencyMs: 182, status: 'degraded' }),
  ])

  render(<CommandCenterPage />)

  expect(await screen.findByText('Singapore')).toBeInTheDocument()
  expect(screen.getByText('Tokyo')).toBeInTheDocument()
  expect(screen.queryByText('sg.example.com')).not.toBeInTheDocument()
  expect(screen.queryByRole('button', { name: /rotate/i })).not.toBeInTheDocument()
  expect(screen.queryByText(/avg latency/i)).not.toBeInTheDocument()
})

test('shows N/A for nodes without physical latency', async () => {
  vi.spyOn(api, 'listNodes').mockResolvedValue([makeNode({ latencyMs: 0 })])

  render(<CommandCenterPage />)

  expect(await screen.findByText('N/A')).toBeInTheDocument()
})

test('shows no nodes found when list is empty', async () => {
  vi.spyOn(api, 'listNodes').mockResolvedValue([])

  render(<CommandCenterPage />)

  expect(await screen.findByText(/no nodes found/i)).toBeInTheDocument()
})

test('shows a load-failure banner instead of a silent empty state when listNodes rejects', async () => {
  vi.spyOn(api, 'listNodes').mockRejectedValue(new Error('nodes failed'))

  render(<CommandCenterPage />)

  expect(await screen.findByText(/failed to load — nodes failed\. reload\./i)).toBeInTheDocument()
  expect(screen.getByText(/no nodes found/i)).toBeInTheDocument()
})

test('shows the session-expired banner when the initial load rejects with session-expired', async () => {
  vi.spyOn(api, 'listNodes').mockRejectedValue(new Error('session-expired'))

  render(<CommandCenterPage />)

  expect(await screen.findByText(/session expired — reload the page/i)).toBeInTheDocument()
})

test('shows a Disabled badge for a disabled node', async () => {
  vi.spyOn(api, 'listNodes').mockResolvedValue([makeNode({ status: 'disabled' })])

  render(<CommandCenterPage />)

  expect(await screen.findByText('Disabled')).toBeInTheDocument()
})
