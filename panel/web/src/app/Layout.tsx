import { useEffect, useState } from 'react'
import { Outlet, useNavigate } from 'react-router-dom'

type Theme = 'dark' | 'light'

function initialTheme(): Theme {
  if (localStorage.theme === 'light' || localStorage.theme === 'dark') return localStorage.theme
  return window.matchMedia?.('(prefers-color-scheme: light)').matches ? 'light' : 'dark'
}

export function Layout() {
  const navigate = useNavigate()
  const [theme, setTheme] = useState<Theme>(initialTheme)

  useEffect(() => {
    document.documentElement.classList.toggle('dark', theme === 'dark')
    localStorage.theme = theme
  }, [theme])

  const toggleTheme = () => setTheme((current) => (current === 'dark' ? 'light' : 'dark'))

  return (
    <div className="min-h-screen bg-slate-50 text-slate-950 dark:bg-slate-950 dark:text-slate-100">
      <header className="flex flex-wrap items-center gap-2 border-b border-slate-200 bg-white/85 p-3 shadow-sm backdrop-blur dark:border-slate-800 dark:bg-slate-950/85 dark:shadow-none">
        <button className="rounded px-2.5 py-1 text-sm text-slate-700 hover:bg-slate-100 dark:text-slate-200 dark:hover:bg-slate-800" onClick={() => navigate('/')}>Home</button>
        <button className="rounded px-2.5 py-1 text-sm text-slate-700 hover:bg-slate-100 dark:text-slate-200 dark:hover:bg-slate-800" onClick={() => navigate('/nodes')}>Nodes</button>
        <button className="rounded px-2.5 py-1 text-sm text-slate-700 hover:bg-slate-100 dark:text-slate-200 dark:hover:bg-slate-800" onClick={() => navigate('/users')}>Users</button>
        <button className="rounded px-2.5 py-1 text-sm text-slate-700 hover:bg-slate-100 dark:text-slate-200 dark:hover:bg-slate-800" onClick={() => navigate('/quick-add')}>Quick Add</button>
        <button className="rounded px-2.5 py-1 text-sm text-slate-700 hover:bg-slate-100 dark:text-slate-200 dark:hover:bg-slate-800" onClick={() => navigate('/events')}>Events</button>
        <button
          type="button"
          onClick={toggleTheme}
          className="ml-auto rounded border border-slate-300 px-3 py-1 text-xs text-slate-700 dark:border-slate-700 dark:text-slate-200"
          aria-label="Toggle theme"
        >
          {theme === 'dark' ? 'Light' : 'Dark'}
        </button>
      </header>
      <main className="p-4">
        <Outlet />
      </main>
    </div>
  )
}
