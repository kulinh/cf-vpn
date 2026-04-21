import { render, screen } from '@testing-library/react'
import { EventsPage } from './EventsPage'
import * as api from '../lib/api'

test('renders latest event rows', async () => {
  vi.spyOn(api, 'listEvents').mockResolvedValue([
    {
      id: 1,
      action: 'node.rotate',
      actor: 'ops@x.com',
      outcome: 'ok',
      ts: 1710000000000,
    },
  ])

  render(<EventsPage />)

  expect(await screen.findByText(/node\.rotate/i)).toBeInTheDocument()
  expect(screen.getByText(/ops@x.com/i)).toBeInTheDocument()
  expect(screen.getByText(/ok/i)).toBeInTheDocument()
})
