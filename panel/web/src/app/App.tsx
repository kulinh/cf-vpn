import { Route, Routes } from 'react-router-dom'
import { CommandCenterPage } from '../pages/CommandCenterPage'
import { EventsPage } from '../pages/EventsPage'
import { NodesPage } from '../pages/NodesPage'
import { QuickAddPage } from '../pages/QuickAddPage'
import { UsersPage } from '../pages/UsersPage'
import { Layout } from './Layout'

export function App() {
  return (
    <Routes>
      <Route element={<Layout />}>
        <Route path="/" element={<CommandCenterPage />} />
        <Route path="/nodes" element={<NodesPage />} />
        <Route path="/users" element={<UsersPage />} />
        <Route path="/quick-add" element={<QuickAddPage />} />
        <Route path="/events" element={<EventsPage />} />
      </Route>
    </Routes>
  )
}
