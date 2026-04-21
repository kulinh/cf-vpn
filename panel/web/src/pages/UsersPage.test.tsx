import { render, screen } from '@testing-library/react'
import { UsersPage } from './UsersPage'
import * as api from '../lib/api'

test('renders users with copy and qr actions', async () => {
  vi.spyOn(api, 'listUsers').mockResolvedValue([
    { id: 'kulinh', name: 'kulinh', nodes: ['HK', 'JP1'] },
  ])

  render(<UsersPage />)

  expect(await screen.findByText('kulinh')).toBeInTheDocument()
  expect(screen.getByRole('button', { name: /copy subscription/i })).toBeInTheDocument()
  expect(screen.getByRole('button', { name: /show qr/i })).toBeInTheDocument()
})
