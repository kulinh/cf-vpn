import { render, screen } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { NotFoundPage } from './NotFoundPage'

describe('NotFoundPage', () => {
  it('names the path and offers the reload that fixes a stale bundle', () => {
    render(
      <MemoryRouter initialEntries={['/connectivity']}>
        <NotFoundPage />
      </MemoryRouter>,
    )
    expect(screen.getByText('/connectivity')).toBeTruthy()
    // A blank page is the failure this replaces; the cause has to be stated.
    expect(screen.getByText(/older version of the panel/i)).toBeTruthy()
    expect(screen.getByRole('button', { name: /reload/i })).toBeTruthy()
  })
})
