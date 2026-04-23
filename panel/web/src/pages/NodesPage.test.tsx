import { fireEvent, render, screen } from '@testing-library/react'
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
  expect(screen.getAllByRole('button', { name: /rotate/i })).toHaveLength(3)
  expect(screen.getAllByRole('button', { name: /check/i })).toHaveLength(3)
  expect(screen.getAllByRole('button', { name: /edit/i })).toHaveLength(3)
  expect(screen.getAllByRole('button', { name: /delete/i })).toHaveLength(3)
})

test('shows no nodes found when list is empty', async () => {
  vi.spyOn(api, 'listNodes').mockResolvedValue([])

  render(<NodesPage />)

  expect(await screen.findByText(/no nodes found/i)).toBeInTheDocument()
})

test('rotate shows loading then success toast', async () => {
  vi.spyOn(api, 'listNodes').mockResolvedValue([makeNode({ id: 'sg' })])
  let resolveRotate: ((value: api.RotateNodeResponse) => void) | null = null
  vi.spyOn(api, 'rotateNode').mockImplementation(
    () =>
      new Promise((resolve) => {
        resolveRotate = resolve
      }),
  )

  render(<NodesPage />)
  await screen.findByText('Singapore')

  fireEvent.click(screen.getByRole('button', { name: /rotate/i }))
  fireEvent.click(screen.getByRole('button', { name: /confirm rotate/i }))

  expect(screen.getByRole('button', { name: /rotating/i })).toBeDisabled()

  resolveRotate?.({ vpnHost: 'new-host.example.com' })

  expect(await screen.findByText(/rotated successfully/i)).toBeInTheDocument()
  expect(screen.getByText(/new-host\.example\.com/i)).toBeInTheDocument()
})

test('shows error toast when rotate fails', async () => {
  vi.spyOn(api, 'listNodes').mockResolvedValue([makeNode({ id: 'sg' })])
  vi.spyOn(api, 'rotateNode').mockRejectedValue(new Error('boom'))

  render(<NodesPage />)
  await screen.findByText('Singapore')

  render(<NodesPage />)

  fireEvent.click(screen.getByRole('button', { name: /rotate/i }))
  fireEvent.click(screen.getByRole('button', { name: /confirm rotate/i }))

  expect(await screen.findByText(/rotate failed/i)).toBeInTheDocument()
})
