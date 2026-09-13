import { useEffect, useRef } from 'react'
import QRCode from 'qrcode'

// A small always-visible QR of one link, so a phone can scan it straight off
// the panel instead of the operator copying text around.
export function QrCode({ value, size = 104 }: { value: string; size?: number }) {
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

  return <canvas ref={canvasRef} className="block rounded bg-white" aria-label={`QR ${value}`} />
}

export type LinkRowProps = {
  // Short name of what the link is (e.g. "RWL-CN"), used in the buttons' accessible names.
  label: string
  // One-line explanation shown under the label.
  hint?: string
  // What lands on the clipboard.
  copyValue: string
  // Custom-scheme deep link to hand to the OS; omitted when the app has none.
  openHref?: string
  onOpen?: (href: string) => void
  onCopy: (value: string, label: string) => void
  // What the QR encodes (defaults to copyValue).
  qrValue?: string
}

export function LinkRow({ label, hint, copyValue, openHref, onOpen, onCopy, qrValue }: LinkRowProps) {
  return (
    <div className="flex items-start gap-3 rounded border border-slate-800 bg-slate-950/40 p-2">
      <QrCode value={qrValue ?? copyValue} />
      <div className="min-w-0 flex-1">
        <p className="text-sm font-medium text-slate-100">{label}</p>
        {hint ? <p className="mt-0.5 text-xs text-slate-400">{hint}</p> : null}
        <p className="mt-1 break-all font-mono text-[11px] leading-snug text-slate-500">{copyValue}</p>
        <div className="mt-2 flex flex-wrap gap-2">
          {openHref && onOpen ? (
            <button
              type="button"
              onClick={() => onOpen(openHref)}
              aria-label={`Open ${label}`}
              className="rounded bg-slate-100 px-3 py-1 text-xs font-medium text-slate-900"
            >
              Open
            </button>
          ) : null}
          <button
            type="button"
            onClick={() => onCopy(copyValue, label)}
            aria-label={`Copy ${label}`}
            className="rounded bg-slate-700 px-3 py-1 text-xs text-slate-100"
          >
            Copy
          </button>
        </div>
      </div>
    </div>
  )
}

export function ClientBlock({ title, accent, children }: { title: string; accent: string; children: React.ReactNode }) {
  return (
    <section className="mt-3 rounded-lg border border-slate-800 p-2">
      <h3 className={`mb-2 text-xs font-semibold uppercase tracking-wide ${accent}`}>{title}</h3>
      <div className="space-y-2">{children}</div>
    </section>
  )
}
