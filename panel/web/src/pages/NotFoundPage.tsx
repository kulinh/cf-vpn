import { btnGhost, btnMd, btnPrimary, card } from '../components/ui/theme'
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
    <section className={`${card} mx-auto max-w-lg space-y-3 p-5`}>
      <h1 className="text-lg font-semibold text-slate-900 dark:text-slate-100">Page not found</h1>
      <p className="text-sm text-slate-600 dark:text-slate-300">
        Nothing is routed at <code className="rounded bg-slate-100 px-1 font-mono dark:bg-slate-800">{location.pathname}</code>.
      </p>
      <p className="text-sm text-slate-600 dark:text-slate-300">
        If you followed a link to a page that should exist, this browser is probably running an
        older version of the panel. Reload to pick up the current one.
      </p>
      <div className="flex gap-2">
        <button
          type="button"
          onClick={() => window.location.reload()}
          className={`${btnPrimary} ${btnMd}`}
        >
          Reload
        </button>
        <button
          type="button"
          onClick={() => navigate('/')}
          className={`${btnGhost} ${btnMd}`}
        >
          Home
        </button>
      </div>
    </section>
  )
}
