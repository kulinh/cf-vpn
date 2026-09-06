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
      className="rounded-lg border border-red-800 bg-red-950/60 px-4 py-3 text-sm text-red-200"
    >
      {message}
    </div>
  )
}
