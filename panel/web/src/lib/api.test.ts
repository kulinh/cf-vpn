import { afterEach, describe, expect, it, vi } from 'vitest'
import { createNode, healthcheckNode, listEvents, listNodes, listUsers, patchNode, rotateNode } from './api'

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

  it('surfaces the agent-provided detail verbatim on a failed rotate', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn().mockResolvedValue(
        jsonResponse(
          { error: 'rotate failed', detail: 'do NOT retry' },
          { status: 500 },
        ),
      ),
    )

    await expect(rotateNode('sg')).rejects.toThrow('do NOT retry')
  })

  it('falls back to the error field, then a generic message, when rotate fails without a detail', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn().mockResolvedValue(jsonResponse({ error: 'agent unreachable' }, { status: 502 })),
    )
    await expect(rotateNode('sg')).rejects.toThrow('agent unreachable')

    vi.stubGlobal(
      'fetch',
      vi.fn().mockResolvedValue(
        new Response('oops', { status: 500, headers: { 'content-type': 'text/plain' } }),
      ),
    )
    await expect(rotateNode('sg')).rejects.toThrow('rotate failed')
  })

  it('passes through a disabled node status', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn().mockResolvedValue(
        jsonResponse([
          {
            id: 'sg',
            label: 'Singapore',
            admin_host: 'sg-admin.example.com',
            vpn_host: 'sg.example.com',
            zone: 'example.com',
            status: 'disabled',
            last_seen_at: null,
            latency_ms: null,
            created_at: 0,
          },
        ]),
      ),
    )

    const nodes = await listNodes()
    expect(nodes[0].status).toBe('disabled')
  })

  it('surfaces the agent-provided detail verbatim on a failed patch', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn().mockResolvedValue(
        jsonResponse({ error: 'patch failed', detail: 'do NOT retry' }, { status: 500 }),
      ),
    )

    await expect(patchNode('sg', { label: 'New label' })).rejects.toThrow('do NOT retry')
  })
})
