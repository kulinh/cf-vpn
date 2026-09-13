import { act, fireEvent, render, screen, within } from '@testing-library/react'
import QRCode from 'qrcode'
import { UsersPage } from './UsersPage'
import * as api from '../lib/api'
import type { Node } from '../lib/types'

vi.mock('qrcode', () => ({
  default: {
    toCanvas: vi.fn().mockResolvedValue(undefined),
  },
}))

const testSubscription: api.UserSubscription = {
  urls: 'vless://u1@hk.example.com:443\nhysteria2://p1@hy2.example.com:21000',
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

test('shows a load-failure banner instead of a silently empty list when the initial load rejects', async () => {
  vi.spyOn(api, 'listUsers').mockRejectedValue(new Error('users failed'))
  vi.spyOn(api, 'listNodes').mockResolvedValue([])

  render(<UsersPage />)

  expect(await screen.findByText(/failed to load — users failed\. reload\./i)).toBeInTheDocument()
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
  expect(await screen.findByText('VN')).toBeInTheDocument()
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

  const deferred: { resolve?: (value: Awaited<ReturnType<typeof api.upgradeUserNodes>>) => void } = {}
  vi.spyOn(api, 'upgradeUserNodes').mockImplementation(
    () =>
      new Promise((resolve) => {
        deferred.resolve = resolve
      }),
  )

  render(<UsersPage />)

  fireEvent.click(await screen.findByRole('button', { name: /sync \(\+1\)/i }))

  expect(screen.getByRole('button', { name: /syncing/i })).toBeDisabled()

  deferred.resolve?.({
    userId: 'kulinh',
    addedNodes: ['VN'],
    addedCount: 1,
    alreadyPresentCount: 4,
    totalNodesAfterUpgrade: 5,
  })

  expect(await screen.findByText(/added 1 nodes/i)).toBeInTheDocument()
})

const anchorSpy = () => vi.spyOn(HTMLAnchorElement.prototype, 'click').mockImplementation(() => {})

async function renderWithSub() {
  vi.spyOn(api, 'listUsers').mockResolvedValue([{ id: 'kulinh', name: 'kulinh', nodes: ['HK'] }])
  vi.spyOn(api, 'listNodes').mockResolvedValue([makeNode('HK')])
  const subSpy = vi.spyOn(api, 'getUserSubscription').mockResolvedValue(testSubscription)
  render(<UsersPage />)
  await screen.findByText('kulinh')
  await vi.waitFor(() => expect(subSpy).toHaveBeenCalledWith('kulinh'))
}

test('renders one block per client with Open / Copy / QR per link, no generic buttons, no inline QR', async () => {
  await renderWithSub()
  expect(await screen.findByText('Shadowrocket')).toBeInTheDocument()
  expect(screen.getByText('Hiddify')).toBeInTheDocument()
  expect(screen.getByText('sing-box')).toBeInTheDocument()
  // 1 sub + 3 confs, 1 hiddify, 3 sing-box profiles = 8 rows.
  expect(screen.getAllByRole('button', { name: /^Copy / })).toHaveLength(8)
  expect(screen.getAllByRole('button', { name: /^QR / })).toHaveLength(8)
  expect(screen.queryByRole('button', { name: /copy subscription/i })).toBeNull()
  expect(screen.queryByRole('button', { name: /show qr/i })).toBeNull()
  expect(screen.queryByText(testSubscription.subUrl)).toBeNull()
  expect(vi.mocked(QRCode.toCanvas)).not.toHaveBeenCalled()
})

test('QR button opens a popup with that link, Close dismisses it', async () => {
  await renderWithSub()
  fireEvent.click(screen.getByRole('button', { name: 'QR sing-box RWL-RU' }))
  const dialog = await screen.findByRole('dialog', { name: 'QR sing-box RWL-RU' })
  const expected = `sing-box://import-remote-profile?url=${encodeURIComponent(`${testSubscription.subUrl}?format=singbox&rules=ru`)}#RWL-RU`
  expect(within(dialog).getByLabelText(`QR image ${expected}`)).toBeInTheDocument()
  expect(vi.mocked(QRCode.toCanvas)).toHaveBeenCalledWith(expect.anything(), expected, expect.anything())
  fireEvent.click(within(dialog).getByRole('button', { name: 'Close' }))
  expect(screen.queryByRole('dialog')).toBeNull()
})

test('Shadowrocket subscription: Open hands a sub:// deep link to a synthetic anchor, Copy puts the plain URL on the clipboard', async () => {
  const writeText = vi.fn().mockResolvedValue(undefined)
  Object.assign(navigator, { clipboard: { writeText } })
  const clickSpy = anchorSpy()
  try {
    await renderWithSub()
    fireEvent.click(screen.getByRole('button', { name: 'Open Shadowrocket subscription RWL' }))
    const anchor = clickSpy.mock.instances[0] as unknown as HTMLAnchorElement
    expect(anchor.href).toBe(`shadowrocket://add/sub://${btoa(testSubscription.subUrl)}?remark=RWL`)
    fireEvent.click(screen.getByRole('button', { name: 'Copy Shadowrocket subscription RWL' }))
    expect(writeText).toHaveBeenCalledWith(testSubscription.subUrl)
    expect(await screen.findByText(/link copied/i)).toBeInTheDocument()
  } finally {
    clickSpy.mockRestore()
  }
})

test('Shadowrocket config rows copy the named .conf URL and have no Open button', async () => {
  const writeText = vi.fn().mockResolvedValue(undefined)
  Object.assign(navigator, { clipboard: { writeText } })
  await renderWithSub()
  fireEvent.click(screen.getByRole('button', { name: 'Copy Shadowrocket config RWL-UAE' }))
  expect(writeText).toHaveBeenCalledWith(`${testSubscription.subUrl}/RWL-UAE.conf`)
  expect(screen.queryByRole('button', { name: 'Open Shadowrocket config RWL-UAE' })).toBeNull()
})

test('Hiddify and sing-box rows open their deep links', async () => {
  const clickSpy = anchorSpy()
  try {
    await renderWithSub()
    fireEvent.click(screen.getByRole('button', { name: 'Open Hiddify import' }))
    fireEvent.click(screen.getByRole('button', { name: 'Open sing-box RWL-UAE' }))
    const hrefs = clickSpy.mock.instances.map((a) => (a as unknown as HTMLAnchorElement).href)
    expect(hrefs[0]).toBe(`hiddify://import/${testSubscription.subUrl}`)
    expect(hrefs[1]).toBe(
      `sing-box://import-remote-profile?url=${encodeURIComponent(`${testSubscription.subUrl}?format=singbox&rules=uae`)}#RWL-UAE`,
    )
  } finally {
    clickSpy.mockRestore()
  }
})
