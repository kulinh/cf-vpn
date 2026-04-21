import { render, screen } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { App } from './App'

test('renders command center shell nav', () => {
  render(
    <MemoryRouter initialEntries={['/']}>
      <App />
    </MemoryRouter>,
  )

  expect(screen.getByRole('button', { name: /command center/i })).toBeInTheDocument()
  expect(screen.getByRole('button', { name: /users/i })).toBeInTheDocument()
  expect(screen.getByRole('button', { name: /events/i })).toBeInTheDocument()
})
