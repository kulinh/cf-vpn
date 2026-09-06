import { useEffect, useState } from 'react'
import { ErrorBanner } from '../components/ui/ErrorBanner'
import { listEvents } from '../lib/api'
import { describeLoadError } from '../lib/errors'
import type { Event } from '../lib/types'

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
    <section className="space-y-3">
      <h1 className="text-xl font-semibold">Events</h1>
      <ErrorBanner message={loadError} />
      <div className="overflow-x-auto rounded-lg border border-slate-800 bg-slate-900">
        <table className="min-w-full text-left text-sm">
          <thead>
            <tr className="border-b border-slate-800 text-xs uppercase tracking-wide text-slate-400">
              <th className="px-3 py-2">Action</th>
              <th className="px-3 py-2">Actor</th>
              <th className="px-3 py-2">Outcome</th>
              <th className="px-3 py-2">Timestamp</th>
            </tr>
          </thead>
          <tbody>
            {events.map((event) => (
              <tr key={event.id} className="border-b border-slate-800/70 last:border-0 text-slate-100">
                <td className="px-3 py-2">{event.action}</td>
                <td className="px-3 py-2">{event.actor}</td>
                <td className="px-3 py-2">{event.outcome}</td>
                <td className="px-3 py-2">{new Date(event.ts).toLocaleString()}</td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </section>
  )
}
