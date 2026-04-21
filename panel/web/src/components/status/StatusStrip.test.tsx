import { fireEvent, render, screen } from '@testing-library/react'
import { StatusStrip } from './StatusStrip'

test('renders four KPI tiles, emits degraded filter, and toggles selected KPI back to all', () => {
  const onFilter = vi.fn()

  render(
    <StatusStrip
      active={3}
      degraded={1}
      down={0}
      avgLatencyMs={182}
      filter="active"
      onFilter={onFilter}
    />,
  )

  fireEvent.click(screen.getByRole('button', { name: /degraded/i }))
  fireEvent.click(screen.getByRole('button', { name: /active/i }))

  expect(onFilter).toHaveBeenNthCalledWith(1, 'degraded')
  expect(onFilter).toHaveBeenNthCalledWith(2, 'all')
  expect(screen.getByText(/182 ms/i)).toBeInTheDocument()
})
