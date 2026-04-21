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
