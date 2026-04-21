import { useEffect, useState } from 'react'
import { listEvents } from '../lib/api'
import type { Event } from '../lib/types'

export function EventsPage() {
  const [events, setEvents] = useState<Event[]>([])

  useEffect(() => {
    let mounted = true

    void listEvents().then((items) => {
      if (mounted) {
        setEvents(items)
      }
    })

    return () => {
      mounted = false
    }
  }, [])

  return (
    <section className="space-y-3">
      <h1 className="text-xl font-semibold">Events</h1>
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
