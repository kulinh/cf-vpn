import { Route, Routes } from 'react-router-dom'
import { CommandCenterPage } from '../pages/CommandCenterPage'
import { Layout } from './Layout'

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
