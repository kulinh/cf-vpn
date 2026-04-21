import { fireEvent, render, screen } from '@testing-library/react'
import { CommandCenterPage } from './CommandCenterPage'

test('defaults to all nodes, filters by status, and resets to all when toggling the selected status', () => {
  render(<CommandCenterPage />)

  expect(screen.getByText(/showing 3 nodes/i)).toBeInTheDocument()

  const degradedFilter = screen.getByRole('button', { name: /degraded/i })

  fireEvent.click(degradedFilter)
  expect(screen.getByText(/showing 1 node/i)).toBeInTheDocument()

  fireEvent.click(degradedFilter)
  expect(screen.getByText(/showing 3 nodes/i)).toBeInTheDocument()
})
