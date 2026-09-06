import { useEffect, useRef } from 'react'
import QRCode from 'qrcode'

type QrModalProps = {
  open: boolean
  userId: string | null
  subscriptionUrl: string | null
  onClose: () => void
}

export function QrModal({ open, userId, subscriptionUrl, onClose }: QrModalProps) {
  const canvasRef = useRef<HTMLCanvasElement>(null)

  useEffect(() => {
    if (!open || !subscriptionUrl || !canvasRef.current) return
    QRCode.toCanvas(canvasRef.current, subscriptionUrl, {
      width: 256,
      margin: 2,
      // QR modules must be dark-on-light for scanners to decode reliably;
      // the modal chrome around the canvas stays dark to match the UI theme.
      color: { dark: '#0f172a', light: '#ffffff' },
    }).catch(() => {
      // ignore
    })
  }, [open, subscriptionUrl])

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

        <div className="mt-4 flex justify-center rounded-lg border border-slate-700 bg-slate-800 p-4">
          <canvas ref={canvasRef} className="block" />
        </div>

        <div className="mt-3 flex justify-end">
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
