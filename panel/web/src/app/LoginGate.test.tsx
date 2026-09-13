import { act, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { vi } from 'vitest'
import { LoginGate } from './LoginGate'
import * as api from '../lib/api'
import * as credentials from '../lib/credentials'

function stubStorage() {
  const data = new Map<string, string>()
  vi.stubGlobal('localStorage', {
    getItem: (k: string) => data.get(k) ?? null,
    setItem: (k: string, v: string) => void data.set(k, v),
    removeItem: (k: string) => void data.delete(k),
    clear: () => data.clear(),
  })
}

describe('LoginGate', () => {
  beforeEach(() => {
    vi.restoreAllMocks()
    vi.unstubAllGlobals()
    stubStorage()
  })

  it('shows the sign-in form when nothing is stored', () => {
    render(
      <LoginGate>
        <div>panel</div>
      </LoginGate>,
    )
    expect(screen.getByRole('button', { name: /sign in/i })).toBeTruthy()
    expect(screen.queryByText('panel')).toBeNull()
  })

  it('renders the panel straight away when credentials are stored', () => {
    credentials.saveCredentials({ username: 'admin', password: 'x' })
    render(
      <LoginGate>
        <div>panel</div>
      </LoginGate>,
    )
    expect(screen.getByText('panel')).toBeTruthy()
  })

  it('stores the credentials and lets the panel through on success', async () => {
    vi.spyOn(api, 'apiFetch').mockResolvedValue(new Response('{}', { status: 200 }))
    render(
      <LoginGate>
        <div>panel</div>
      </LoginGate>,
    )
    fireEvent.change(screen.getByLabelText(/username/i), { target: { value: 'admin' } })
    fireEvent.change(screen.getByLabelText(/password/i), { target: { value: 'linh@1234' } })
    fireEvent.click(screen.getByRole('button', { name: /sign in/i }))

    await screen.findByText('panel')
    expect(credentials.loadCredentials()).toEqual({ username: 'admin', password: 'linh@1234' })
  })

  it('reports a wrong password and keeps nothing behind', async () => {
    vi.spyOn(api, 'apiFetch').mockRejectedValue(new api.UnauthorizedError())
    render(
      <LoginGate>
        <div>panel</div>
      </LoginGate>,
    )
    fireEvent.change(screen.getByLabelText(/username/i), { target: { value: 'admin' } })
    fireEvent.change(screen.getByLabelText(/password/i), { target: { value: 'nope' } })
    fireEvent.click(screen.getByRole('button', { name: /sign in/i }))

    await screen.findByText(/wrong username or password/i)
    // A rejected attempt must not leave a bad entry that breaks the next load.
    expect(credentials.loadCredentials()).toBeNull()
    expect(screen.queryByText('panel')).toBeNull()
  })

  it('distinguishes an unreachable API from a wrong password', async () => {
    vi.spyOn(api, 'apiFetch').mockRejectedValue(new TypeError('Failed to fetch'))
    render(
      <LoginGate>
        <div>panel</div>
      </LoginGate>,
    )
    fireEvent.change(screen.getByLabelText(/username/i), { target: { value: 'admin' } })
    fireEvent.change(screen.getByLabelText(/password/i), { target: { value: 'x' } })
    fireEvent.click(screen.getByRole('button', { name: /sign in/i }))

    await screen.findByText(/could not reach the panel api/i)
  })

  it('returns to the form when any later call reports 401', async () => {
    credentials.saveCredentials({ username: 'admin', password: 'x' })
    render(
      <LoginGate>
        <div>panel</div>
      </LoginGate>,
    )
    expect(screen.getByText('panel')).toBeTruthy()
    act(() => {
      window.dispatchEvent(new Event('panel-unauthorized'))
    })
    await waitFor(() => expect(screen.queryByText('panel')).toBeNull())
    expect(screen.getByRole('button', { name: /sign in/i })).toBeTruthy()
  })
})
