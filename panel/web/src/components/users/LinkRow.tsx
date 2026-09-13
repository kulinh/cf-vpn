import { useEffect, useRef } from 'react'
import QRCode from 'qrcode'

// A small always-visible QR of one link, so a phone can scan it straight off
// the panel instead of the operator copying text around.
export function QrCode({ value, size = 256 }: { value: string; size?: number }) {
  const canvasRef = useRef<HTMLCanvasElement>(null)

  useEffect(() => {
    if (!canvasRef.current) return
    QRCode.toCanvas(canvasRef.current, value, {
      width: size,
      margin: 1,
      // Dark-on-light decodes reliably; the surrounding chrome stays dark.
      color: { dark: '#0f172a', light: '#ffffff' },
    }).catch(() => {
      // ignore
    })
  }, [value, size])

  return <canvas ref={canvasRef} className="block" aria-label={`QR image ${value}`} />
}

export type LinkRowProps = {
  // Short name shown on the row ("RWL-CN"); the block title gives the context.
  label: string
  // Used in the buttons' accessible names ("Copy Shadowrocket config RWL-CN").
  fullLabel: string
  copyValue: string
  // Custom-scheme deep link to hand to the OS; omitted when the app has none.
  openHref?: string
  onOpen?: (href: string) => void
  onCopy: (value: string, label: string) => void
  // What the QR encodes (defaults to copyValue).
  qrValue?: string
  onShowQr: (value: string, title: string) => void
}

// One compact line per link: name on the left, Open / Copy / QR on the right.
// Nothing else — the page is used on a phone.
export function LinkRow({ label, fullLabel, copyValue, openHref, onOpen, onCopy, qrValue, onShowQr }: LinkRowProps) {
  const btn = 'rounded px-3 py-1 text-xs'
  return (
    <div className="flex items-center justify-between gap-2 rounded border border-slate-800 bg-slate-950/40 px-2 py-1.5">
      <span className="truncate text-sm text-slate-100">{label}</span>
      <div className="flex shrink-0 gap-1.5">
        {openHref && onOpen ? (
          <button type="button" onClick={() => onOpen(openHref)} aria-label={`Open ${fullLabel}`} className={`${btn} bg-slate-100 font-medium text-slate-900`}>
            Open
          </button>
        ) : null}
        <button type="button" onClick={() => onCopy(copyValue, fullLabel)} aria-label={`Copy ${fullLabel}`} className={`${btn} bg-slate-700 text-slate-100`}>
          Copy
        </button>
        <button type="button" onClick={() => onShowQr(qrValue ?? copyValue, fullLabel)} aria-label={`QR ${fullLabel}`} className={`${btn} bg-slate-700 text-slate-100`}>
          QR
        </button>
      </div>
    </div>
  )
}

export function ClientBlock({ title, accent, children }: { title: string; accent: string; children: React.ReactNode }) {
  return (
    <section className="mt-3 flex min-w-0 flex-col rounded-lg border border-slate-800 p-2 md:mt-0">
      <h3 className={`mb-2 text-xs font-semibold uppercase tracking-wide ${accent}`}>{title}</h3>
      <div className="space-y-1.5">{children}</div>
    </section>
  )
}

// The QR popup: one link, big enough to scan from another phone.
export function QrPopup({ value, title, onClose }: { value: string | null; title: string; onClose: () => void }) {
  if (!value) return null
  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/60 p-4" onClick={onClose} role="dialog" aria-label={`QR ${title}`}>
      <div className="w-full max-w-xs rounded-xl border border-slate-700 bg-slate-900 p-4" onClick={(e) => e.stopPropagation()}>
        <p className="text-sm font-medium text-slate-100">{title}</p>
        <div className="mt-3 flex justify-center rounded-lg bg-white p-3">
          <QrCode value={value} size={256} />
        </div>
        <div className="mt-3 flex justify-end">
          <button type="button" onClick={onClose} className="rounded bg-slate-700 px-3 py-1.5 text-sm text-slate-100">
            Close
          </button>
        </div>
      </div>
    </div>
  )
}
