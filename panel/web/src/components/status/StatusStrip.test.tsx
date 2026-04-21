import { fireEvent, render, screen } from '@testing-library/react'
import { StatusStrip } from './StatusStrip'

test('renders status strip and emits canonical filter values including all reset', () => {
  const onFilter = vi.fn()

  render(
    <StatusStrip
      counts={{ active: 3, disabled: 1, unreachable: 0 }}
      avgLatencyMs={182}
      filter="active"
      onFilter={onFilter}
    />,
  )

  fireEvent.click(screen.getByRole('button', { name: /degraded/i }))
  fireEvent.click(screen.getByRole('button', { name: /all/i }))

  expect(onFilter).toHaveBeenNthCalledWith(1, 'disabled')
  expect(onFilter).toHaveBeenNthCalledWith(2, 'all')
  expect(screen.getByText(/182 ms/i)).toBeInTheDocument()
})
