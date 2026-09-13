import { useEffect, useRef } from 'react'
import QRCode from 'qrcode'

// A QR of one link, big enough to scan from another phone.
export function QrCode({ value, size = 256 }: { value: string; size?: number }) {
  const canvasRef = useRef<HTMLCanvasElement>(null)

  useEffect(() => {
    if (!canvasRef.current) return
    QRCode.toCanvas(canvasRef.current, value, {
      width: size,
      margin: 1,
      // Dark-on-light decodes reliably; the surrounding chrome follows the theme.
      color: { dark: '#0f172a', light: '#ffffff' },
    }).catch(() => {
      // ignore
    })
  }, [value, size])

  return <canvas ref={canvasRef} className="block" aria-label={`QR image ${value}`} />
}

// Colour per client app. Tailwind only ships classes it can see literally,
// so every variant is spelled out here instead of being built from a string.
export type Tone = 'sky' | 'emerald' | 'violet'

const TONES: Record<Tone, { block: string; title: string; dot: string; open: string }> = {
  sky: {
    block: 'border-sky-200 bg-sky-50/70 dark:border-sky-900/60 dark:bg-sky-950/20',
    title: 'text-sky-700 dark:text-sky-300',
    dot: 'bg-sky-500',
    open: 'bg-sky-600 text-white hover:bg-sky-700 dark:bg-sky-500 dark:hover:bg-sky-400 dark:text-slate-950',
  },
  emerald: {
    block: 'border-emerald-200 bg-emerald-50/70 dark:border-emerald-900/60 dark:bg-emerald-950/20',
    title: 'text-emerald-700 dark:text-emerald-300',
    dot: 'bg-emerald-500',
    open: 'bg-emerald-600 text-white hover:bg-emerald-700 dark:bg-emerald-500 dark:hover:bg-emerald-400 dark:text-slate-950',
  },
  violet: {
    block: 'border-violet-200 bg-violet-50/70 dark:border-violet-900/60 dark:bg-violet-950/20',
    title: 'text-violet-700 dark:text-violet-300',
    dot: 'bg-violet-500',
    open: 'bg-violet-600 text-white hover:bg-violet-700 dark:bg-violet-500 dark:hover:bg-violet-400 dark:text-slate-950',
  },
}

// Colour per rule set, so RWL-CN / RWL-UAE / RWL-RU are told apart at a glance.
const LIST_BADGE: Record<string, string> = {
  CN: 'bg-rose-100 text-rose-700 ring-rose-200 dark:bg-rose-500/15 dark:text-rose-300 dark:ring-rose-500/30',
  UAE: 'bg-amber-100 text-amber-800 ring-amber-200 dark:bg-amber-500/15 dark:text-amber-300 dark:ring-amber-500/30',
  RU: 'bg-blue-100 text-blue-700 ring-blue-200 dark:bg-blue-500/15 dark:text-blue-300 dark:ring-blue-500/30',
}

export type LinkRowProps = {
  // Short name shown on the row ("RWL-CN", "Subscription RWL").
  label: string
  // Used in the buttons' accessible names ("Copy Shadowrocket config RWL-CN").
  fullLabel: string
  tone: Tone
  // Rule-set badge (CN / UAE / RU); omitted for rows that carry no rules.
  list?: string
  copyValue: string
  // Custom-scheme deep link to hand to the OS; omitted when the app has none.
  openHref?: string
  onOpen?: (href: string) => void
  onCopy: (value: string, label: string) => void
  // What the QR encodes (defaults to copyValue).
  qrValue?: string
  onShowQr: (value: string, title: string) => void
}

const ghost =
  'rounded-md border border-slate-200 bg-white px-2.5 py-1 text-xs font-medium text-slate-700 hover:bg-slate-50 ' +
  'dark:border-slate-700 dark:bg-slate-800 dark:text-slate-200 dark:hover:bg-slate-700'

// One compact line per link: name on the left, Open / Copy / QR on the right.
export function LinkRow({ label, fullLabel, tone, list, copyValue, openHref, onOpen, onCopy, qrValue, onShowQr }: LinkRowProps) {
  return (
    <div className="flex items-center justify-between gap-2 rounded-md border border-slate-200 bg-white px-2 py-1.5 shadow-sm dark:border-slate-800 dark:bg-slate-900 dark:shadow-none">
      <span className="flex min-w-0 items-center gap-1.5 text-sm font-medium text-slate-800 dark:text-slate-100">
        {list ? (
          <span className={`shrink-0 rounded px-1.5 py-0.5 text-[10px] font-bold ring-1 ring-inset ${LIST_BADGE[list] ?? ''}`}>{list}</span>
        ) : null}
        <span className="truncate">{label}</span>
      </span>
      <div className="flex shrink-0 gap-1.5">
        {openHref && onOpen ? (
          <button
            type="button"
            onClick={() => onOpen(openHref)}
            aria-label={`Open ${fullLabel}`}
            className={`rounded-md px-2.5 py-1 text-xs font-semibold ${TONES[tone].open}`}
          >
            Open
          </button>
        ) : null}
        <button type="button" onClick={() => onCopy(copyValue, fullLabel)} aria-label={`Copy ${fullLabel}`} className={ghost}>
          Copy
        </button>
        <button type="button" onClick={() => onShowQr(qrValue ?? copyValue, fullLabel)} aria-label={`QR ${fullLabel}`} className={ghost}>
          QR
        </button>
      </div>
    </div>
  )
}

export function ClientBlock({ title, tone, children }: { title: string; tone: Tone; children: React.ReactNode }) {
  return (
    <section className={`flex min-w-0 flex-col rounded-lg border p-2 ${TONES[tone].block}`}>
      <h3 className={`mb-2 flex items-center gap-1.5 text-xs font-bold uppercase tracking-wide ${TONES[tone].title}`}>
        <span className={`h-2 w-2 rounded-full ${TONES[tone].dot}`} aria-hidden="true" />
        {title}
      </h3>
      <div className="space-y-1.5">{children}</div>
    </section>
  )
}

// The QR popup.
export function QrPopup({ value, title, onClose }: { value: string | null; title: string; onClose: () => void }) {
  if (!value) return null
  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-slate-950/50 p-4 backdrop-blur-sm" onClick={onClose} role="dialog" aria-label={`QR ${title}`}>
      <div
        className="w-full max-w-xs rounded-xl border border-slate-200 bg-white p-4 shadow-xl dark:border-slate-700 dark:bg-slate-900"
        onClick={(e) => e.stopPropagation()}
      >
        <p className="text-sm font-semibold text-slate-900 dark:text-slate-100">{title}</p>
        <div className="mt-3 flex justify-center rounded-lg border border-slate-200 bg-white p-3 dark:border-slate-700">
          <QrCode value={value} size={256} />
        </div>
        <div className="mt-3 flex justify-end">
          <button type="button" onClick={onClose} className={ghost}>
            Close
          </button>
        </div>
      </div>
    </div>
  )
}
