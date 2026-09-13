import { useEffect, useState } from 'react'
import { ErrorBanner } from '../components/ui/ErrorBanner'
import { Badge, PageHeader, tableWrap, td, th, thead, tr, type Tone } from '../components/ui/theme'
import { listEvents } from '../lib/api'
import { describeLoadError } from '../lib/errors'
import type { Event } from '../lib/types'

function outcomeTone(outcome: string): Tone {
  if (outcome === 'ok') return 'green'
  if (outcome === 'error') return 'red'
  if (outcome === 'partial') return 'amber'
  return 'slate'
}

export function EventsPage() {
  const [events, setEvents] = useState<Event[]>([])
  const [loadError, setLoadError] = useState<string | null>(null)

  useEffect(() => {
    let mounted = true

    listEvents()
      .then((items) => {
        if (mounted) {
          setEvents(items)
        }
      })
      .catch((error: unknown) => {
        if (mounted) setLoadError(describeLoadError(error))
      })

    return () => {
      mounted = false
    }
  }, [])

  return (
    <section className="space-y-4">
      <PageHeader title="Events" subtitle="Audit log of panel and cron actions." />
      <ErrorBanner message={loadError} />
      <div className={tableWrap}>
        <table className="min-w-full text-left text-sm">
          <thead className={thead}>
            <tr>
              <th className={th}>Action</th>
              <th className={th}>Actor</th>
              <th className={th}>Outcome</th>
              <th className={th}>Timestamp</th>
            </tr>
          </thead>
          <tbody>
            {events.map((event) => (
              <tr key={event.id} className={tr}>
                <td className={`${td} font-mono text-xs text-slate-800 dark:text-slate-200`}>{event.action}</td>
                <td className={`${td} text-slate-600 dark:text-slate-300`}>{event.actor}</td>
                <td className={td}>
                  <Badge tone={outcomeTone(event.outcome)} withDot>
                    {event.outcome}
                  </Badge>
                </td>
                <td className={`${td} tabular-nums text-slate-500 dark:text-slate-400`}>{new Date(event.ts).toLocaleString()}</td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </section>
  )
}
