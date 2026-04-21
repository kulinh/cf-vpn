type QrModalProps = {
  open: boolean
  userId: string | null
  onClose: () => void
}

export function QrModal({ open, userId, onClose }: QrModalProps) {
  if (!open || !userId) {
    return null
  }

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/60 p-4" onClick={onClose}>
      <div
        className="w-full max-w-sm rounded-xl border border-slate-700 bg-slate-900 p-4"
        onClick={(event) => event.stopPropagation()}
      >
        <h2 className="text-lg font-semibold text-slate-100">Subscription QR</h2>
        <p className="mt-1 text-sm text-slate-400">User: {userId}</p>

        <div className="mt-4 rounded-lg border border-dashed border-slate-600 bg-slate-800 p-6 text-center text-xs text-slate-300">
          QR placeholder for {userId}
        </div>

        <div className="mt-4 flex justify-end">
          <button
            type="button"
            onClick={onClose}
            className="rounded bg-slate-700 px-3 py-1.5 text-sm text-slate-100"
          >
            Close
          </button>
        </div>
      </div>
    </div>
  )
}
