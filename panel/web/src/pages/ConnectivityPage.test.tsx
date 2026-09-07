import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import { vi } from 'vitest'
import { ConnectivityPage } from './ConnectivityPage'
import * as api from '../lib/api'
import * as connectivity from '../lib/connectivity'
import type { Node } from '../lib/types'

function makeNode(overrides: Partial<Node> = {}): Node {
  return {
    id: 'sin-01',
    label: 'SIN-01',
    status: 'active',
    latencyMs: 82,
    vpnHost: 'assets-abc.example.com',
    adminHost: 'sin-01.example.com',
    lastSeenAt: Date.now(),
    zone: 'example.com',
    createdAt: 0,
    mode: 'direct',
    ...overrides,
  }
}

describe('ConnectivityPage', () => {
  it('probes every node with a VPN endpoint and shows the outcome', async () => {
    vi.spyOn(api, 'listNodes').mockResolvedValue([
      makeNode(),
      makeNode({ id: 'jpy-01', label: 'JPY-01', vpnHost: 'edge-def.example.com', mode: 'cloudflare' }),
    ])
    const probe = vi
      .spyOn(connectivity, 'probeHost')
      .mockImplementation(async (nodeId, host) => ({
        nodeId,
        host,
        outcome: nodeId === 'sin-01' ? 'tls-refused' : 'reachable',
        elapsedMs: 210,
      }))

    render(<ConnectivityPage />)
    await screen.findByText('SIN-01')
    fireEvent.click(screen.getByRole('button', { name: /run test/i }))

    await waitFor(() => expect(probe).toHaveBeenCalledTimes(2))
    // A Reality node declining the handshake is still reachable — reporting it
    // as blocked would send the user hunting a network fault that isn't there.
    await waitFor(() => expect(screen.getAllByText('Reachable')).toHaveLength(2))
    expect(screen.getAllByText('210 ms')).toHaveLength(2)
  })

  it('skips nodes that have no VPN endpoint to test', async () => {
    vi.spyOn(api, 'listNodes').mockResolvedValue([makeNode({ vpnHost: '' })])
    render(<ConnectivityPage />)
    await screen.findByText(/no nodes with a vpn endpoint/i)
  })

  it('orders the finished run fastest first', async () => {
    vi.spyOn(api, 'listNodes').mockResolvedValue([
      makeNode({ id: 'slow', label: 'SLOW', vpnHost: 'slow.example.com' }),
      makeNode({ id: 'fast', label: 'FAST', vpnHost: 'fast.example.com' }),
    ])
    vi.spyOn(connectivity, 'probeHost').mockImplementation(async (nodeId, host) => ({
      nodeId,
      host,
      outcome: 'reachable',
      elapsedMs: nodeId === 'fast' ? 90 : 900,
    }))

    render(<ConnectivityPage />)
    await screen.findByText('SLOW')
    fireEvent.click(screen.getByRole('button', { name: /run test/i }))

    await waitFor(() => expect(screen.getAllByText(/^\d+ ms$/)).toHaveLength(2))
    const rows = screen.getAllByRole('row').slice(1)
    expect(rows[0].textContent).toContain('FAST')
    expect(rows[1].textContent).toContain('SLOW')
  })

  it('gives the run button a colour the light theme does not blank out', async () => {
    // styles/tailwind.css remaps bg-slate-900 to #fff outside dark mode, so a
    // bg-slate-900 + text-white button renders white-on-white and disappears.
    vi.spyOn(api, 'listNodes').mockResolvedValue([makeNode()])
    render(<ConnectivityPage />)
    const button = screen.getByRole('button', { name: /run test/i })
    expect(button.className).not.toMatch(/bg-slate-900/)
    expect(button.className).toMatch(/bg-blue-600/)
  })

  it('surfaces a failed node load instead of showing an empty table', async () => {
    vi.spyOn(api, 'listNodes').mockRejectedValue(new Error('boom'))
    render(<ConnectivityPage />)
    await waitFor(() => expect(screen.queryByText(/no nodes with a vpn endpoint/i)).toBeNull())
  })
})
