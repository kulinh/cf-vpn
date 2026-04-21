import { fireEvent, render, screen } from '@testing-library/react'
import { QuickUserPanel } from './QuickUserPanel'

test('searches user and exposes copy + qr actions', () => {
  const onCopy = vi.fn()
  const onQr = vi.fn()

  render(
    <QuickUserPanel
      users={[{ id: 'kulinh', name: 'kulinh', nodes: ['SG', 'JP1'] }]}
      onCopy={onCopy}
      onShowQr={onQr}
    />,
  )

  fireEvent.change(screen.getByPlaceholderText(/search user/i), { target: { value: 'ku' } })
  fireEvent.click(screen.getByRole('button', { name: /copy subscription/i }))

  expect(onCopy).toHaveBeenCalledWith('kulinh')
  expect(screen.getByRole('button', { name: /show qr/i })).toBeInTheDocument()
})
