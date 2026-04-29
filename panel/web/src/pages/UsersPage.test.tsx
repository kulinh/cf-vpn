import { fireEvent, render, screen } from '@testing-library/react'
import { UsersPage } from './UsersPage'
import * as api from '../lib/api'
import type { Node } from '../lib/types'

const testSubscription: api.UserSubscription = {
  urls: 'vless://u1@hk.example.com:443\ntrojan://p1@hk.example.com:443',
  token: 'a'.repeat(32),
  subUrl: `http://localhost:3000/sub/${'a'.repeat(32)}`,
}

function makeNode(id: string): Node {
  return {
    id,
    label: id,
    status: 'active',
    latencyMs: 10,
    vpnHost: `${id.toLowerCase()}.example.com`,
    adminHost: `${id.toLowerCase()}-admin.example.com`,
    lastSeenAt: null,
    zone: 'example.com',
    createdAt: 0,
  }
}

test('renders users with copy and qr actions', async () => {
  vi.spyOn(api, 'listUsers').mockResolvedValue([{ id: 'kulinh', name: 'kulinh', nodes: ['HK', 'JP1'] }])
  vi.spyOn(api, 'listNodes').mockResolvedValue([makeNode('HK'), makeNode('JP1')])

  render(<UsersPage />)

  expect(await screen.findByText('kulinh')).toBeInTheDocument()
  expect(screen.getByRole('button', { name: /copy subscription/i })).toBeInTheDocument()
  expect(screen.getByRole('button', { name: /show qr/i })).toBeInTheDocument()
})

test('shows Sync (+N) and Up-to-date states per user', async () => {
  vi.spyOn(api, 'listUsers').mockResolvedValue([
    { id: 'kulinh', name: 'kulinh', nodes: ['HK', 'JP1', 'JP2', 'SG'] },
    { id: 'minh', name: 'minh', nodes: ['HK', 'JP1', 'JP2', 'SG', 'VN'] },
  ])
  vi.spyOn(api, 'listNodes').mockResolvedValue([
    makeNode('HK'),
    makeNode('JP1'),
    makeNode('JP2'),
    makeNode('SG'),
    makeNode('VN'),
  ])

  render(<UsersPage />)

  expect(await screen.findByText('kulinh')).toBeInTheDocument()
  expect(screen.getByRole('button', { name: /sync \(\+1\)/i })).toBeEnabled()
  expect(screen.getByRole('button', { name: /up-to-date/i })).toBeDisabled()
})

test('syncs user with missing node directly without confirm dialog', async () => {
  vi.spyOn(api, 'listUsers').mockResolvedValue([
    { id: 'kulinh', name: 'kulinh', nodes: ['HK', 'JP1', 'JP2', 'SG'] },
  ])
  vi.spyOn(api, 'listNodes').mockResolvedValue([
    makeNode('HK'),
    makeNode('JP1'),
    makeNode('JP2'),
    makeNode('SG'),
    makeNode('VN'),
  ])
  const syncSpy = vi.spyOn(api, 'upgradeUserNodes').mockResolvedValue({
    userId: 'kulinh',
    addedNodes: ['VN'],
    addedCount: 1,
    alreadyPresentCount: 4,
    totalNodesAfterUpgrade: 5,
  })

  render(<UsersPage />)

  fireEvent.click(await screen.findByRole('button', { name: /sync \(\+1\)/i }))

  expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
  expect(syncSpy).toHaveBeenCalledWith('kulinh')
  expect(await screen.findByText(/added 1 nodes/i)).toBeInTheDocument()
  expect(screen.getByText(/nodes: hk, jp1, jp2, sg, vn/i)).toBeInTheDocument()
  expect(screen.getByRole('button', { name: /up-to-date/i })).toBeDisabled()
})

test('shows error toast when sync fails', async () => {
  vi.spyOn(api, 'listUsers').mockResolvedValue([
    { id: 'kulinh', name: 'kulinh', nodes: ['HK', 'JP1', 'JP2', 'SG'] },
  ])
  vi.spyOn(api, 'listNodes').mockResolvedValue([
    makeNode('HK'),
    makeNode('JP1'),
    makeNode('JP2'),
    makeNode('SG'),
    makeNode('VN'),
  ])
  vi.spyOn(api, 'upgradeUserNodes').mockRejectedValue(new Error('boom'))

  render(<UsersPage />)

  fireEvent.click(await screen.findByRole('button', { name: /sync \(\+1\)/i }))

  expect(await screen.findByText(/sync failed/i)).toBeInTheDocument()
  expect(screen.getByRole('button', { name: /sync \(\+1\)/i })).toBeEnabled()
})

test('shows no-op toast when user is already up-to-date after sync', async () => {
  vi.spyOn(api, 'listUsers').mockResolvedValue([
    { id: 'kulinh', name: 'kulinh', nodes: ['HK', 'JP1', 'JP2', 'SG'] },
  ])
  vi.spyOn(api, 'listNodes').mockResolvedValue([
    makeNode('HK'),
    makeNode('JP1'),
    makeNode('JP2'),
    makeNode('SG'),
    makeNode('VN'),
  ])
  vi.spyOn(api, 'upgradeUserNodes').mockResolvedValue({
    userId: 'kulinh',
    addedNodes: [],
    addedCount: 0,
    alreadyPresentCount: 5,
    totalNodesAfterUpgrade: 5,
  })

  render(<UsersPage />)

  fireEvent.click(await screen.findByRole('button', { name: /sync \(\+1\)/i }))

  expect(await screen.findByText(/user is already up-to-date/i)).toBeInTheDocument()
})

test('matches node ids case-insensitively for sync eligibility', async () => {
  vi.spyOn(api, 'listUsers').mockResolvedValue([
    { id: 'kulinh', name: 'kulinh', nodes: ['HK', 'jp1'] },
  ])
  vi.spyOn(api, 'listNodes').mockResolvedValue([makeNode('hk'), makeNode('JP1'), makeNode('VN')])

  render(<UsersPage />)

  expect(await screen.findByRole('button', { name: /sync \(\+1\)/i })).toBeInTheDocument()
})

test('copy subscription writes sub URL to clipboard', async () => {
  vi.spyOn(api, 'listUsers').mockResolvedValue([{ id: 'kulinh', name: 'kulinh', nodes: ['HK'] }])
  vi.spyOn(api, 'listNodes').mockResolvedValue([makeNode('HK')])
  vi.spyOn(api, 'getUserSubscription').mockResolvedValue(testSubscription)

  const writeText = vi.fn().mockResolvedValue(undefined)
  Object.assign(navigator, { clipboard: { writeText } })

  render(<UsersPage />)

  fireEvent.click(await screen.findByRole('button', { name: /copy subscription/i }))

  expect(await screen.findByText(/subscription url copied/i)).toBeInTheDocument()
  expect(writeText).toHaveBeenCalledWith(testSubscription.subUrl)
})

test('Shadowrocket button navigates to shadowrocket deep link with base64 sub URL', async () => {
  vi.spyOn(api, 'listUsers').mockResolvedValue([{ id: 'kulinh', name: 'kulinh', nodes: ['HK'] }])
  vi.spyOn(api, 'listNodes').mockResolvedValue([makeNode('HK')])
  const subSpy = vi.spyOn(api, 'getUserSubscription').mockResolvedValue(testSubscription)

  const hrefSetter = vi.fn()
  const originalLocation = window.location
  Object.defineProperty(window, 'location', {
    configurable: true,
    value: {
      ...originalLocation,
      get href() {
        return originalLocation.href
      },
      set href(value: string) {
        hrefSetter(value)
      },
    },
  })

  try {
    render(<UsersPage />)

    await screen.findByText('kulinh')
    await vi.waitFor(() => expect(subSpy).toHaveBeenCalledWith('kulinh'))

    fireEvent.click(screen.getByRole('button', { name: /shadowrocket/i }))

    expect(hrefSetter).toHaveBeenCalledWith(`shadowrocket://add/sub/${btoa(testSubscription.subUrl)}`)
  } finally {
    Object.defineProperty(window, 'location', { configurable: true, value: originalLocation })
  }
})

test('V2rayNG button navigates to v2rayng deep link with URL-encoded sub URL', async () => {
  vi.spyOn(api, 'listUsers').mockResolvedValue([{ id: 'kulinh', name: 'kulinh', nodes: ['HK'] }])
  vi.spyOn(api, 'listNodes').mockResolvedValue([makeNode('HK')])
  const subSpy = vi.spyOn(api, 'getUserSubscription').mockResolvedValue(testSubscription)

  const hrefSetter = vi.fn()
  const originalLocation = window.location
  Object.defineProperty(window, 'location', {
    configurable: true,
    value: {
      ...originalLocation,
      get href() {
        return originalLocation.href
      },
      set href(value: string) {
        hrefSetter(value)
      },
    },
  })

  try {
    render(<UsersPage />)

    await screen.findByText('kulinh')
    await vi.waitFor(() => expect(subSpy).toHaveBeenCalledWith('kulinh'))

    fireEvent.click(screen.getByRole('button', { name: /v2rayng/i }))

    expect(hrefSetter).toHaveBeenCalledWith(
      `v2rayng://install-sub?url=${encodeURIComponent(testSubscription.subUrl)}#${encodeURIComponent('RWL8899')}`,
    )
  } finally {
    Object.defineProperty(window, 'location', { configurable: true, value: originalLocation })
  }
})

test('shows Syncing... while request is pending', async () => {
  vi.spyOn(api, 'listUsers').mockResolvedValue([
    { id: 'kulinh', name: 'kulinh', nodes: ['HK', 'JP1', 'JP2', 'SG'] },
  ])
  vi.spyOn(api, 'listNodes').mockResolvedValue([
    makeNode('HK'),
    makeNode('JP1'),
    makeNode('JP2'),
    makeNode('SG'),
    makeNode('VN'),
  ])

  let resolveSync: ((value: Awaited<ReturnType<typeof api.upgradeUserNodes>>) => void) | null = null
  vi.spyOn(api, 'upgradeUserNodes').mockImplementation(
    () =>
      new Promise((resolve) => {
        resolveSync = resolve
      }),
  )

  render(<UsersPage />)

  fireEvent.click(await screen.findByRole('button', { name: /sync \(\+1\)/i }))

  expect(screen.getByRole('button', { name: /syncing/i })).toBeDisabled()

  resolveSync?.({
    userId: 'kulinh',
    addedNodes: ['VN'],
    addedCount: 1,
    alreadyPresentCount: 4,
    totalNodesAfterUpgrade: 5,
  })

  expect(await screen.findByText(/added 1 nodes/i)).toBeInTheDocument()
})
