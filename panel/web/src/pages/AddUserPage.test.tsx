import { fireEvent, render, screen } from '@testing-library/react'
import { AddUserPage } from './AddUserPage'
import * as api from '../lib/api'

test('submits add user form with API payload', async () => {
  const createUser = vi.spyOn(api, 'createUser').mockResolvedValue()

  render(<AddUserPage />)

  fireEvent.change(screen.getByLabelText(/name/i), { target: { value: 'call2vn' } })
  fireEvent.click(screen.getByRole('button', { name: /add user/i }))

  expect(createUser).toHaveBeenCalledWith({ name: 'call2vn' })
  expect(await screen.findByText(/user added/i)).toBeInTheDocument()
})
