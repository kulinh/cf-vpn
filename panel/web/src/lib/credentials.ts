/**
 * Panel credentials for the Worker's HTTP basic auth.
 *
 * The Worker answers an unauthenticated /api/* with 401 + WWW-Authenticate,
 * which most browsers turn into a native login dialog. Relying on that dialog
 * is a trap: several iOS Safari versions suppress the auth prompt for requests
 * issued by fetch/XHR, so the panel would simply fail to load on a phone with
 * no way for the user to enter anything. Sending the header ourselves behaves
 * the same everywhere.
 *
 * This is deliberately a low-security store: the credentials sit in
 * localStorage, readable by any script on the origin. That matches what the
 * basic-auth setup is — a gate against strangers, not a secret-management
 * system. Do not extend it to hold anything that matters more than this.
 */

const STORAGE_KEY = 'panel-credentials'

export type Credentials = { username: string; password: string }

export function encodeBasic({ username, password }: Credentials): string {
  return `Basic ${btoa(`${username}:${password}`)}`
}

export function loadCredentials(): Credentials | null {
  let raw: string | null = null
  try {
    raw = localStorage.getItem(STORAGE_KEY)
  } catch {
    // Storage blocked (private mode, site-data disabled). Treat as signed out
    // rather than crashing the whole app on boot.
    return null
  }
  if (!raw) return null
  try {
    const parsed = JSON.parse(raw) as Partial<Credentials>
    if (typeof parsed?.username !== 'string' || typeof parsed?.password !== 'string') return null
    if (parsed.username === '') return null
    return { username: parsed.username, password: parsed.password }
  } catch {
    // Corrupt entry: drop it instead of wedging the app in a broken state.
    clearCredentials()
    return null
  }
}

export function saveCredentials(credentials: Credentials): void {
  try {
    localStorage.setItem(STORAGE_KEY, JSON.stringify(credentials))
  } catch {
    // Not fatal — the session keeps working from memory, it just won't survive
    // a reload.
  }
}

export function clearCredentials(): void {
  try {
    localStorage.removeItem(STORAGE_KEY)
  } catch {
    // nothing to do
  }
}
