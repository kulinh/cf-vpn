import { fireEvent, render, screen } from '@testing-library/react'
import { CommandCenterPage } from './CommandCenterPage'

test('defaults to all nodes, filters by status, and resets to all', () => {
  render(<CommandCenterPage />)

  expect(screen.getByText(/showing 3 nodes/i)).toBeInTheDocument()

  fireEvent.click(screen.getByRole('button', { name: /degraded/i }))
  expect(screen.getByText(/showing 1 node/i)).toBeInTheDocument()

  fireEvent.click(screen.getByRole('button', { name: /all/i }))
  expect(screen.getByText(/showing 3 nodes/i)).toBeInTheDocument()
})
