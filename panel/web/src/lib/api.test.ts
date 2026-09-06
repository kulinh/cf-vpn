import { afterEach, describe, expect, it, vi } from 'vitest'
import { createNode, healthcheckNode, listEvents, listNodes, listUsers } from './api'

function jsonResponse(body: unknown, init: ResponseInit = {}): Response {
  return new Response(JSON.stringify(body), {
    status: 200,
    headers: { 'content-type': 'application/json' },
    ...init,
  })
}

// jsdom/undici's Response doesn't let us set `redirected` via the
// constructor (it's a real navigation-derived flag), so an Access-style
// "redirected to an HTML login page" response is simulated with an HTML
// content-type on an otherwise-200 response — the other half of the
// `isSessionExpired` check in lib/api.ts.
function htmlLoginResponse(): Response {
  return new Response('<html>login</html>', {
    status: 200,
    headers: { 'content-type': 'text/html' },
  })
}

describe('lib/api session handling', () => {
  afterEach(() => {
    vi.unstubAllGlobals()
  })

  it('parses a normal JSON success response', async () => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(jsonResponse([{ id: 'u1', name: 'u1', nodes: [] }])))

    const users = await listUsers()
    expect(users).toEqual([{ id: 'u1', name: 'u1', nodes: [] }])
  })

  it('throws a plain failure error for a non-2xx JSON error response', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn().mockResolvedValue(
        new Response('oops', { status: 500, headers: { 'content-type': 'text/plain' } }),
      ),
    )

    await expect(listUsers()).rejects.toThrow('users failed')
  })

  it('throws a distinct session-expired error when an Access login page comes back as 200 OK HTML', async () => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(htmlLoginResponse()))

    await expect(listUsers()).rejects.toThrow('session-expired')
    await expect(listNodes()).rejects.toThrow('session-expired')
    await expect(listEvents()).rejects.toThrow('session-expired')
    await expect(healthcheckNode('sg')).rejects.toThrow('session-expired')
  })

  it('treats a mutation response redirected to the Access login page as session-expired, not silent success', async () => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(htmlLoginResponse()))

    await expect(createNode({ id: 'sg', label: 'Singapore' })).rejects.toThrow('session-expired')
  })
})
