import { render, screen } from '@testing-library/react'
import { StatusStrip } from './StatusStrip'

test('renders four KPI tiles and emits filter callback', () => {
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

  screen.getByRole('button', { name: /degraded/i }).click()

  expect(onFilter).toHaveBeenCalledWith('degraded')
  expect(screen.getByText(/182 ms/i)).toBeInTheDocument()
})
