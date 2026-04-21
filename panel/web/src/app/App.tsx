import { Route, Routes } from 'react-router-dom'
import { CommandCenterPage } from '../pages/CommandCenterPage'
import { EventsPage } from '../pages/EventsPage'
import { UsersPage } from '../pages/UsersPage'
import { Layout } from './Layout'

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
