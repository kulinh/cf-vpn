import type { ReactNode } from 'react'

// Shared look for every page, written with explicit light AND dark classes.
// (styles/tailwind.css still remaps a few dark-only slate classes for older
// markup; nothing here relies on that remap.)

export const card =
  'rounded-xl border border-slate-200 bg-white shadow-sm dark:border-slate-800 dark:bg-slate-900/60 dark:shadow-none'

export const tableWrap = `${card} overflow-x-auto`
export const thead =
  'bg-slate-50 text-[11px] font-semibold uppercase tracking-wide text-slate-500 dark:bg-slate-900 dark:text-slate-400'
export const th = 'whitespace-nowrap px-4 py-2.5 text-left'
export const tr =
  'border-t border-slate-100 transition-colors hover:bg-slate-50/80 dark:border-slate-800 dark:hover:bg-slate-800/40'
export const td = 'px-4 py-2.5'

const btnBase =
  'inline-flex items-center justify-center rounded-md font-medium transition focus:outline-none focus-visible:ring-2 focus-visible:ring-offset-2 dark:focus-visible:ring-offset-slate-950 disabled:cursor-not-allowed disabled:opacity-60'

export const btnPrimary = `${btnBase} bg-indigo-600 text-white shadow-sm hover:bg-indigo-700 focus-visible:ring-indigo-500 dark:bg-indigo-500 dark:hover:bg-indigo-400`
export const btnGhost = `${btnBase} border border-slate-200 bg-white text-slate-700 hover:bg-slate-50 focus-visible:ring-slate-400 dark:border-slate-700 dark:bg-slate-800 dark:text-slate-200 dark:hover:bg-slate-700`
export const btnDanger = `${btnBase} border border-rose-200 bg-rose-50 text-rose-700 hover:bg-rose-100 focus-visible:ring-rose-500 dark:border-rose-500/30 dark:bg-rose-500/10 dark:text-rose-300 dark:hover:bg-rose-500/20`
export const btnSm = 'px-2.5 py-1 text-xs'
export const btnMd = 'px-3.5 py-2 text-sm'

export const input =
  'w-full rounded-md border border-slate-300 bg-white px-3 py-2 text-sm text-slate-900 placeholder:text-slate-400 shadow-sm focus:border-indigo-500 focus:outline-none focus:ring-2 focus:ring-indigo-500/30 dark:border-slate-700 dark:bg-slate-950 dark:text-slate-100 dark:placeholder:text-slate-500'
export const label = 'mb-1 block text-xs font-medium text-slate-600 dark:text-slate-400'

export const mono = 'font-mono text-xs text-slate-600 dark:text-slate-400'
export const muted = 'text-slate-500 dark:text-slate-400'

export function PageHeader({ title, subtitle, actions }: { title: string; subtitle?: string; actions?: ReactNode }) {
  return (
    <div className="flex flex-wrap items-end justify-between gap-3">
      <div>
        <h1 className="text-xl font-semibold text-slate-900 dark:text-slate-100">{title}</h1>
        {subtitle ? <p className="mt-1 text-sm text-slate-500 dark:text-slate-400">{subtitle}</p> : null}
      </div>
      {actions ? <div className="flex flex-wrap gap-2">{actions}</div> : null}
    </div>
  )
}

const pill = 'inline-flex items-center gap-1 whitespace-nowrap rounded-full px-2 py-0.5 text-xs font-medium ring-1 ring-inset'

export const tone = {
  green: 'bg-emerald-50 text-emerald-700 ring-emerald-200 dark:bg-emerald-500/10 dark:text-emerald-300 dark:ring-emerald-500/30',
  red: 'bg-rose-50 text-rose-700 ring-rose-200 dark:bg-rose-500/10 dark:text-rose-300 dark:ring-rose-500/30',
  amber: 'bg-amber-50 text-amber-800 ring-amber-200 dark:bg-amber-500/10 dark:text-amber-300 dark:ring-amber-500/30',
  sky: 'bg-sky-50 text-sky-700 ring-sky-200 dark:bg-sky-500/10 dark:text-sky-300 dark:ring-sky-500/30',
  orange: 'bg-orange-50 text-orange-700 ring-orange-200 dark:bg-orange-500/10 dark:text-orange-300 dark:ring-orange-500/30',
  slate: 'bg-slate-100 text-slate-600 ring-slate-200 dark:bg-slate-800 dark:text-slate-300 dark:ring-slate-700',
} as const

export type Tone = keyof typeof tone

const dot: Record<Tone, string> = {
  green: 'bg-emerald-500',
  red: 'bg-rose-500',
  amber: 'bg-amber-500',
  sky: 'bg-sky-500',
  orange: 'bg-orange-500',
  slate: 'bg-slate-400',
}

export function Badge({ tone: t, children, withDot = false, title }: { tone: Tone; children: ReactNode; withDot?: boolean; title?: string }) {
  return (
    <span className={`${pill} ${tone[t]}`} title={title}>
      {withDot ? <span className={`h-1.5 w-1.5 rounded-full ${dot[t]}`} aria-hidden="true" /> : null}
      {children}
    </span>
  )
}

export function statusTone(status: string): Tone {
  switch (status) {
    case 'active':
      return 'green'
    case 'degraded':
      return 'amber'
    case 'down':
    case 'unreachable':
      return 'red'
    default:
      return 'slate'
  }
}

// direct = the node answers on its own IP; cloudflare = behind a tunnel.
export function ModeBadge({ mode }: { mode: string | null | undefined }) {
  const m = mode ?? 'direct'
  return <Badge tone={m === 'cloudflare' ? 'orange' : 'sky'}>{m}</Badge>
}

// Worker→agent round trip through the admin tunnel: a healthy node answers in
// 0.5–1.5 s from the Cloudflare edge, so the bands are wide on purpose — a
// tight "under 300 ms" colouring painted the whole healthy fleet red.
export function LatencyText({ ms, pending = false }: { ms: number | null | undefined; pending?: boolean }) {
  if (pending) return <span className={muted}>...</span>
  if (ms == null || ms <= 0) return <span className={muted}>N/A</span>
  const colour =
    ms < 1500 ? 'text-emerald-600 dark:text-emerald-400' : ms < 3000 ? 'text-amber-600 dark:text-amber-400' : 'text-rose-600 dark:text-rose-400'
  return <span className={`whitespace-nowrap font-medium tabular-nums ${colour}`}>{`${ms} ms`}</span>
}

export function StatCard({ label: l, value, accent }: { label: string; value: ReactNode; accent: string }) {
  return (
    <div className={`${card} relative overflow-hidden p-3`}>
      <span className={`absolute inset-y-0 left-0 w-1 ${accent}`} aria-hidden="true" />
      <p className="pl-2 text-[11px] font-semibold uppercase tracking-wide text-slate-500 dark:text-slate-400">{l}</p>
      <p className="mt-1 pl-2 text-2xl font-semibold tabular-nums text-slate-900 dark:text-slate-100">{value}</p>
    </div>
  )
}
