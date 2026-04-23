import { render, screen } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { App } from './App'
import * as api from '../lib/api'

test('renders command center shell nav', () => {
  vi.spyOn(api, 'listUsers').mockResolvedValue([])
  vi.spyOn(api, 'listNodes').mockResolvedValue([])

  render(
    <MemoryRouter initialEntries={['/']}>
      <App />
    </MemoryRouter>,
  )

  expect(screen.getByRole('button', { name: /command center/i })).toBeInTheDocument()
  expect(screen.getByRole('button', { name: /users/i })).toBeInTheDocument()
  expect(screen.getByRole('button', { name: /events/i })).toBeInTheDocument()
  expect(screen.getByRole('button', { name: /add nodes/i })).toBeInTheDocument()
  expect(screen.getByRole('button', { name: /add user/i })).toBeInTheDocument()
})
