type ToastProps = {
  message: string | null
  onClose?: () => void
}

export function Toast({ message, onClose }: ToastProps) {
  if (!message) {
    return null
  }

  return (
    <div
      role="status"
      aria-live="polite"
      className="fixed bottom-4 left-1/2 z-50 w-[min(90vw,28rem)] -translate-x-1/2 rounded-lg border border-zinc-800 bg-zinc-900 px-4 py-3 text-sm text-white shadow-lg dark:border-slate-600 dark:bg-slate-700"
    >
      <div className="flex items-center justify-between gap-3">
        <span>{message}</span>
        {onClose ? (
          <button
            type="button"
            onClick={onClose}
            className="rounded-md bg-white/10 px-2 py-1 text-xs text-white hover:bg-white/20"
          >
            Dismiss
          </button>
        ) : null}
      </div>
    </div>
  )
}
