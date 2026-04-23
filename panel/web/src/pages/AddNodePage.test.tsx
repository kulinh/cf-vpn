import { fireEvent, render, screen } from '@testing-library/react'
import { AddNodePage } from './AddNodePage'
import * as api from '../lib/api'

test('submits add node form with API payload', async () => {
  const createNode = vi.spyOn(api, 'createNode').mockResolvedValue()

  render(<AddNodePage />)

  fireEvent.change(screen.getByLabelText(/id/i), { target: { value: 'SG2' } })
  fireEvent.change(screen.getByLabelText(/label/i), { target: { value: 'Singapore 2' } })
  fireEvent.change(screen.getByLabelText(/admin host/i), { target: { value: 'admin-sg2.rwl265.com' } })
  fireEvent.change(screen.getByLabelText(/vpn host/i), { target: { value: 'sg2.rwl265.com' } })
  fireEvent.change(screen.getByLabelText(/zone/i), { target: { value: 'rwl265.com' } })

  fireEvent.click(screen.getByRole('button', { name: /add node/i }))

  expect(createNode).toHaveBeenCalledWith({
    id: 'SG2',
    label: 'Singapore 2',
    adminHost: 'admin-sg2.rwl265.com',
    vpnHost: 'sg2.rwl265.com',
    zone: 'rwl265.com',
  })
  expect(await screen.findByText(/node added/i)).toBeInTheDocument()
})
