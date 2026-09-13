type ErrorBannerProps = {
  message: string | null
}

// Rendered in place of (or above) a page's normal content when the initial
// load promise chain rejects, so a failed fetch is never indistinguishable
// from a genuinely empty list (H17).
export function ErrorBanner({ message }: ErrorBannerProps) {
  if (!message) {
    return null
  }

  return (
    <div
      role="alert"
      className="rounded-lg border border-rose-200 bg-rose-50 px-4 py-3 text-sm text-rose-800 dark:border-rose-500/30 dark:bg-rose-500/10 dark:text-rose-200"
    >
      {message}
    </div>
  )
}
