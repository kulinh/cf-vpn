import { useCallback, useEffect, useState, type FormEvent, type ReactNode } from 'react'
import { UnauthorizedError, apiFetch } from '../lib/api'
import { clearCredentials, loadCredentials, saveCredentials } from '../lib/credentials'

/**
 * Sign-in gate for the panel's basic auth.
 *
 * The app owns this screen rather than leaving it to the browser's native 401
 * dialog: iOS Safari suppresses that dialog for fetch/XHR in several versions,
 * which would leave the panel permanently blank on a phone with nothing to
 * type into.
 */
export function LoginGate({ children }: { children: ReactNode }) {
  const [authed, setAuthed] = useState(() => loadCredentials() !== null)
  const [username, setUsername] = useState('')
  const [password, setPassword] = useState('')
  const [error, setError] = useState<string | null>(null)
  const [checking, setChecking] = useState(false)

  // Any API call that comes back 401 has already dropped the stored
  // credentials; bring the user back here instead of leaving every page
  // showing its own load error.
  useEffect(() => {
    const onUnauthorized = () => setAuthed(false)
    window.addEventListener('panel-unauthorized', onUnauthorized)
    return () => window.removeEventListener('panel-unauthorized', onUnauthorized)
  }, [])

  const submit = useCallback(
    async (event: FormEvent) => {
      event.preventDefault()
      setChecking(true)
      setError(null)
      // Store first so apiFetch picks the credentials up, then verify against a
      // cheap endpoint. Wrong credentials are cleared by apiFetch's 401 path,
      // so a failed attempt never leaves a bad entry behind.
      saveCredentials({ username, password })
      try {
        const response = await apiFetch('/api/me')
        if (!response.ok) throw new Error('sign-in failed')
        setAuthed(true)
        setPassword('')
      } catch (err: unknown) {
        clearCredentials()
        setError(
          err instanceof UnauthorizedError
            ? 'Wrong username or password.'
            : 'Could not reach the panel API. Try again.',
        )
      } finally {
        setChecking(false)
      }
    },
    [username, password],
  )

  if (authed) return <>{children}</>

  return (
    <div className="flex min-h-screen items-center justify-center bg-slate-50 p-4 text-slate-950 dark:bg-slate-950 dark:text-slate-100">
      <form
        onSubmit={submit}
        className="w-full max-w-sm space-y-3 rounded-xl border border-slate-200 bg-white p-5 shadow-sm dark:border-slate-800 dark:bg-slate-900"
      >
        <h1 className="text-lg font-semibold">cf-vpn Control Panel</h1>
        <label className="block text-sm">
          <span className="mb-1 block text-slate-600 dark:text-slate-300">Username</span>
          <input
            value={username}
            onChange={(e) => setUsername(e.target.value)}
            autoComplete="username"
            autoCapitalize="none"
            autoCorrect="off"
            required
            className="w-full rounded border border-slate-300 bg-white px-3 py-2 text-slate-950 dark:border-slate-700 dark:bg-slate-950 dark:text-slate-100"
          />
        </label>
        <label className="block text-sm">
          <span className="mb-1 block text-slate-600 dark:text-slate-300">Password</span>
          <input
            type="password"
            value={password}
            onChange={(e) => setPassword(e.target.value)}
            autoComplete="current-password"
            required
            className="w-full rounded border border-slate-300 bg-white px-3 py-2 text-slate-950 dark:border-slate-700 dark:bg-slate-950 dark:text-slate-100"
          />
        </label>
        {error ? <p className="text-sm text-rose-600 dark:text-rose-400">{error}</p> : null}
        <button
          type="submit"
          disabled={checking}
          className="w-full rounded bg-blue-600 px-4 py-2 text-sm font-medium text-white transition hover:bg-blue-700 disabled:opacity-60"
        >
          {checking ? 'Signing in…' : 'Sign in'}
        </button>
      </form>
    </div>
  )
}
