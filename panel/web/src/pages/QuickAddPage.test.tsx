import { fireEvent, render, screen } from '@testing-library/react'
import { QuickAddPage } from './QuickAddPage'
import * as api from '../lib/api'

test('renders add node and add user forms', () => {
  render(<QuickAddPage />)

  expect(screen.getAllByRole('button', { name: /add node/i })).toHaveLength(1)
  expect(screen.getAllByRole('button', { name: /add user/i })).toHaveLength(1)
  expect(screen.getAllByRole('button', { name: /random/i })).toHaveLength(2)
})

test('add node shows validation toast when fields are empty', () => {
  render(<QuickAddPage />)

  fireEvent.click(screen.getAllByRole('button', { name: /add node/i })[0])

  expect(screen.getByText('All node fields are required')).toBeInTheDocument()
})

test('add user shows validation toast when name is empty', () => {
  render(<QuickAddPage />)

  fireEvent.click(screen.getAllByRole('button', { name: /add user/i })[0])

  expect(screen.getByText('User name is required')).toBeInTheDocument()
})

test('adds node successfully with all fields filled', async () => {
  const createNodeSpy = vi.spyOn(api, 'createNode').mockResolvedValue()

  render(<QuickAddPage />)

  const inputs = screen.getAllByRole('textbox')
  fireEvent.change(inputs[0], { target: { value: 'sg-01' } }) // ID
  fireEvent.change(inputs[1], { target: { value: 'Singapore' } }) // Label
  fireEvent.change(inputs[2], { target: { value: 'ab12cd34.rwl265.com' } }) // Admin Host
  fireEvent.change(inputs[3], { target: { value: 'ab12cd34.rwl265.com' } }) // VPN Host
  fireEvent.change(inputs[4], { target: { value: 'rwl265.com' } }) // Zone

  fireEvent.click(screen.getAllByRole('button', { name: /add node/i })[0])

  expect(await screen.findByText('Node added successfully')).toBeInTheDocument()
  expect(createNodeSpy).toHaveBeenCalledWith({
    id: 'sg-01',
    label: 'Singapore',
    admin_host: 'ab12cd34.rwl265.com',
    vpn_host: 'ab12cd34.rwl265.com',
    zone: 'rwl265.com',
  })
})

test('adds user successfully', async () => {
  const createUserSpy = vi.spyOn(api, 'createUser').mockResolvedValue()

  render(<QuickAddPage />)

  fireEvent.change(screen.getByPlaceholderText('e.g. John Doe'), { target: { value: 'John Doe' } })
  fireEvent.click(screen.getAllByRole('button', { name: /add user/i })[0])

  expect(await screen.findByText('User added successfully')).toBeInTheDocument()
  expect(createUserSpy).toHaveBeenCalledWith({ name: 'John Doe' })
})

test('shows error toast when add node fails', async () => {
  vi.spyOn(api, 'createNode').mockRejectedValue(new Error('boom'))

  render(<QuickAddPage />)

  const inputs = screen.getAllByRole('textbox')
  fireEvent.change(inputs[0], { target: { value: 'sg-01' } })
  fireEvent.change(inputs[1], { target: { value: 'Singapore' } })
  fireEvent.change(inputs[2], { target: { value: 'admin.example.com' } })
  fireEvent.change(inputs[3], { target: { value: 'vpn.example.com' } })
  fireEvent.change(inputs[4], { target: { value: 'rwl265.com' } })

  fireEvent.click(screen.getAllByRole('button', { name: /add node/i })[0])

  expect(await screen.findByText('Failed to add node')).toBeInTheDocument()
})

test('random buttons fill the admin host and vpn host fields', () => {
  render(<QuickAddPage />)

  const randomButtons = screen.getAllByRole('button', { name: /random/i })

  fireEvent.click(randomButtons[0])
  fireEvent.click(randomButtons[1])

  const inputs = screen.getAllByRole('textbox')
  const adminValue = (inputs[2] as HTMLInputElement).value
  const vpnValue = (inputs[3] as HTMLInputElement).value
  expect(adminValue).toMatch(/^[0-9a-f]{8}\./)
  expect(vpnValue).toMatch(/^[0-9a-f]{8}\./)
})
