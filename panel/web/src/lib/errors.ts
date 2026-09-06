// Shared formatting for the "initial page load failed" banner (H17) and the
// Cloudflare Access session-expiry case surfaced by lib/api.ts (M-R1). Kept
// in one place so every page shows the same wording for the same failure.
export function describeLoadError(error: unknown): string {
  if (error instanceof Error && error.message === 'session-expired') {
    return 'Session expired — reload the page'
  }
  const message = error instanceof Error ? error.message : 'unknown error'
  return `Failed to load — ${message}. Reload.`
}
