import { Route, Routes } from 'react-router-dom'
import { Layout } from './Layout'

function CommandCenterPage() {
  return <h1 className="text-xl font-semibold">Command Center</h1>
}

function UsersPage() {
  return <h1 className="text-xl font-semibold">Users</h1>
}

function EventsPage() {
  return <h1 className="text-xl font-semibold">Events</h1>
}

export function App() {
  return (
    <Routes>
      <Route element={<Layout />}>
        <Route path="/" element={<CommandCenterPage />} />
        <Route path="/users" element={<UsersPage />} />
        <Route path="/events" element={<EventsPage />} />
      </Route>
    </Routes>
  )
}
