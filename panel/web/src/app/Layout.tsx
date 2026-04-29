import { Outlet, useNavigate } from 'react-router-dom'

export function Layout() {
  const navigate = useNavigate()

  return (
    <div className="min-h-screen bg-slate-950 text-slate-100">
      <header className="flex gap-2 border-b border-slate-800 p-3">
        <button onClick={() => navigate('/')}>Command Center</button>
        <button onClick={() => navigate('/nodes')}>Nodes</button>
        <button onClick={() => navigate('/users')}>Users</button>
        <button onClick={() => navigate('/quick-add')}>Add Nodes</button>
        <button onClick={() => navigate('/quick-add')}>Add User</button>
        <button onClick={() => navigate('/events')}>Events</button>
      </header>
      <main className="p-4">
        <Outlet />
      </main>
    </div>
  )
}
