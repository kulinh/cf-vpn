import { render } from '@testing-library/react'
import QRCode from 'qrcode'
import { QrModal } from './QrModal'

vi.mock('qrcode', () => ({
  default: {
    toCanvas: vi.fn().mockResolvedValue(undefined),
  },
}))

test('renders the QR with dark modules on a light background so scanners can decode it', () => {
  render(
    <QrModal
      open
      userId="kulinh"
      subscriptionUrl="https://cp.example.com/sub/abc123"
      onClose={() => {}}
    />,
  )

  expect(QRCode.toCanvas).toHaveBeenCalledWith(
    expect.anything(),
    'https://cp.example.com/sub/abc123',
    expect.objectContaining({
      color: { dark: '#0f172a', light: '#ffffff' },
    }),
  )
})
