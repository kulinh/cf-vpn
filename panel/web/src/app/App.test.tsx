import { render, screen } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { App } from './App'
import * as api from '../lib/api'

test('renders command center shell nav', async () => {
  vi.spyOn(api, 'listUsers').mockResolvedValue([])
  vi.spyOn(api, 'listNodes').mockResolvedValue([])

  render(
    <MemoryRouter initialEntries={['/']}>
      <App />
    </MemoryRouter>,
  )

  expect(screen.getByRole('button', { name: /^home$/i })).toBeInTheDocument()
  expect(screen.getByRole('button', { name: /users/i })).toBeInTheDocument()
  expect(screen.getByRole('button', { name: /events/i })).toBeInTheDocument()
  expect(screen.getByRole('button', { name: /quick add/i })).toBeInTheDocument()
  expect(screen.getByRole('button', { name: /toggle theme/i })).toBeInTheDocument()
  expect(screen.queryByRole('button', { name: /add nodes/i })).not.toBeInTheDocument()
  expect(screen.queryByRole('button', { name: /add user/i })).not.toBeInTheDocument()
  expect(await screen.findByText(/no nodes found/i)).toBeInTheDocument()
})
