import { useEffect, useState } from 'react'
import { Outlet, useLocation, useNavigate } from 'react-router-dom'

type Theme = 'dark' | 'light'

function initialTheme(): Theme {
  try {
    const stored = localStorage.getItem('theme')
    if (stored === 'light' || stored === 'dark') return stored
  } catch {
    // storage blocked (e.g. Safari private mode) — fall through to media query
  }
  return window.matchMedia?.('(prefers-color-scheme: light)').matches ? 'light' : 'dark'
}

const NAV: ReadonlyArray<readonly [string, string]> = [
  ['/', 'Home'],
  ['/nodes', 'Nodes'],
  ['/users', 'Users'],
  ['/quick-add', 'Quick Add'],
  ['/events', 'Events'],
  ['/connectivity', 'Connectivity'],
]

export function Layout() {
  const navigate = useNavigate()
  const { pathname } = useLocation()
  const [theme, setTheme] = useState<Theme>(initialTheme)

  useEffect(() => {
    document.documentElement.classList.toggle('dark', theme === 'dark')
    try {
      localStorage.setItem('theme', theme)
    } catch {
      // storage blocked — theme just won't persist across reloads
    }
  }, [theme])

  const toggleTheme = () => setTheme((current) => (current === 'dark' ? 'light' : 'dark'))

  return (
    <div className="min-h-screen bg-slate-50 text-slate-900 dark:bg-slate-950 dark:text-slate-100">
      <header className="sticky top-0 z-40 border-b border-slate-200 bg-white/85 backdrop-blur dark:border-slate-800 dark:bg-slate-950/85">
        <div className="mx-auto flex max-w-7xl flex-wrap items-center gap-1 px-4 py-2">
          <span className="mr-2 flex items-center gap-2 text-sm font-bold tracking-tight text-slate-900 dark:text-slate-100">
            <span className="h-5 w-5 rounded-md bg-gradient-to-br from-indigo-500 to-fuchsia-500 shadow-sm" aria-hidden="true" />
            RWL
          </span>
          {NAV.map(([to, name]) => {
            const active = to === '/' ? pathname === '/' : pathname.startsWith(to)
            return (
              <button
                key={to}
                aria-current={active ? 'page' : undefined}
                className={
                  active
                    ? 'rounded-md bg-indigo-50 px-2.5 py-1 text-sm font-medium text-indigo-700 dark:bg-indigo-500/15 dark:text-indigo-300'
                    : 'rounded-md px-2.5 py-1 text-sm text-slate-600 hover:bg-slate-100 hover:text-slate-900 dark:text-slate-300 dark:hover:bg-slate-800 dark:hover:text-slate-100'
                }
                onClick={() => navigate(to)}
              >
                {name}
              </button>
            )
          })}
          <button
            type="button"
            onClick={toggleTheme}
            className="ml-auto rounded-md border border-slate-200 bg-white px-3 py-1 text-xs font-medium text-slate-700 hover:bg-slate-50 dark:border-slate-700 dark:bg-slate-900 dark:text-slate-200 dark:hover:bg-slate-800"
            aria-label="Toggle theme"
          >
            {theme === 'dark' ? '☀ Light' : '☾ Dark'}
          </button>
        </div>
      </header>
      <main className="mx-auto max-w-7xl p-4">
        <Outlet />
      </main>
    </div>
  )
}
