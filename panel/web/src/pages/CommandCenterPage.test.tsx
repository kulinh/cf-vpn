import { fireEvent, render, screen } from '@testing-library/react'
import { CommandCenterPage } from './CommandCenterPage'
import * as api from '../lib/api'

test('defaults to all nodes, filters by status, and resets to all when toggling the selected status', () => {
  render(<CommandCenterPage />)

  expect(screen.getByText(/showing 3 nodes/i)).toBeInTheDocument()

  const degradedFilter = screen.getByRole('button', { name: /degraded/i })

  fireEvent.click(degradedFilter)
  expect(screen.getByText(/showing 1 node/i)).toBeInTheDocument()

  fireEvent.click(degradedFilter)
  expect(screen.getByText(/showing 3 nodes/i)).toBeInTheDocument()
})

test('rotate node shows loading then success toast', async () => {
  type RotateApiContractResponse = {
    new_host: string
    tunnel_uuid?: string
  }

  const rotateApiResponse: RotateApiContractResponse = {
    new_host: 'new-host.example.com',
    tunnel_uuid: 'tunnel-123',
  }

  let resolveRotate: ((value: api.RotateNodeResponse) => void) | null = null

  vi.spyOn(api, 'rotateNode').mockImplementation(
    () =>
      new Promise((resolve) => {
        resolveRotate = resolve
      }),
  )

  render(<CommandCenterPage />)

  fireEvent.click(screen.getAllByRole('button', { name: /rotate/i })[0])
  fireEvent.click(screen.getByRole('button', { name: /confirm rotate/i }))

  expect(screen.getByRole('button', { name: /rotating/i })).toBeDisabled()

  resolveRotate?.({
    vpnHost: rotateApiResponse.new_host,
    tunnelUuid: rotateApiResponse.tunnel_uuid,
  })

  expect(await screen.findByText(/rotated successfully/i)).toBeInTheDocument()
  expect(screen.getByText(/new-host\.example\.com/i)).toBeInTheDocument()
})

