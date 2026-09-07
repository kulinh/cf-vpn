import { useLocation, useNavigate } from 'react-router-dom'

/**
 * Catch-all route.
 *
 * Without one, an unmatched path renders the Layout with an empty <Outlet/> —
 * the nav bar with a blank page under it. That is indistinguishable from a
 * broken page, and it is exactly what a stale bundle produces: the browser
 * holds an older index.html, so a route added in a later deploy does not exist
 * in the JS that is running, and the new page silently renders as nothing.
 *
 * Saying so, and offering the reload that fixes it, turns the worst failure
 * mode into a self-explaining one.
 */
export function NotFoundPage() {
  const location = useLocation()
  const navigate = useNavigate()

  return (
    <section className="space-y-3">
      <h1 className="text-lg font-semibold">Page not found</h1>
      <p className="text-sm text-slate-600 dark:text-slate-300">
        Nothing is routed at <code className="font-mono">{location.pathname}</code>.
      </p>
      <p className="text-sm text-slate-600 dark:text-slate-300">
        If you followed a link to a page that should exist, this browser is probably running an
        older version of the panel. Reload to pick up the current one.
      </p>
      <div className="flex gap-2">
        <button
          type="button"
          onClick={() => window.location.reload()}
          className="rounded bg-blue-600 px-4 py-2 text-sm font-medium text-white transition hover:bg-blue-700"
        >
          Reload
        </button>
        <button
          type="button"
          onClick={() => navigate('/')}
          className="rounded border border-slate-300 px-3 py-1.5 text-sm dark:border-slate-700"
        >
          Home
        </button>
      </div>
    </section>
  )
}
