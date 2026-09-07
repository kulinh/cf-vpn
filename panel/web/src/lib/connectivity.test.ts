import { describe, expect, it, vi } from 'vitest'
import { FAST_FAIL_MS, classifyProbe, describeOutcome, probeHost } from './connectivity'

describe('classifyProbe', () => {
  it('treats a completed request as reachable', () => {
    expect(classifyProbe(true, 120)).toBe('reachable')
    // Slowness alone never demotes a request that actually completed.
    expect(classifyProbe(true, FAST_FAIL_MS + 5000)).toBe('reachable')
  })

  it('reads a fast rejection as a refused handshake, not a blocked path', () => {
    // A Reality node offers the camouflage host's certificate; the browser
    // rejects it in well under a second. TCP got through, so the node is
    // reachable from this network.
    expect(classifyProbe(false, 400)).toBe('tls-refused')
  })

  it('reads a slow rejection as blocked', () => {
    expect(classifyProbe(false, FAST_FAIL_MS + 1)).toBe('blocked')
  })

  it('puts the boundary between the two rejection kinds at FAST_FAIL_MS', () => {
    expect(classifyProbe(false, FAST_FAIL_MS - 1)).toBe('tls-refused')
    expect(classifyProbe(false, FAST_FAIL_MS)).toBe('blocked')
  })
})

describe('describeOutcome', () => {
  it('never describes a refused handshake as a failure to reach the node', () => {
    const text = describeOutcome('tls-refused')
    expect(text).toMatch(/Reachable/)
    expect(text).toMatch(/not blocked/i)
    expect(text).not.toMatch(/No answer|timed? out/i)
  })

  it('does not claim certainty about why a blocked probe failed', () => {
    // DNS failure and a dropped packet are indistinguishable from here, and
    // the wording must not pick one.
    expect(describeOutcome('blocked')).toMatch(/does not resolve/)
  })
})

describe('probeHost', () => {
  const clock = (...ticks: number[]) => {
    let i = 0
    return () => ticks[Math.min(i++, ticks.length - 1)]
  }

  it('reports elapsed time and marks a completed request reachable', async () => {
    const fetchImpl = vi.fn().mockResolvedValue(new Response(null))
    const result = await probeHost('JPY-01', 'edge-abc.example.com', {
      fetchImpl: fetchImpl as unknown as typeof fetch,
      now: clock(1000, 1180),
    })
    expect(result).toMatchObject({ nodeId: 'JPY-01', outcome: 'reachable', elapsedMs: 180 })
  })

  it('requests no-cors and bypasses the cache', async () => {
    // A cached answer would report a path that is no longer open as reachable.
    const fetchImpl = vi.fn().mockResolvedValue(new Response(null))
    await probeHost('N', 'h.example.com', {
      fetchImpl: fetchImpl as unknown as typeof fetch,
      now: clock(0, 10),
    })
    const [url, init] = fetchImpl.mock.calls[0]
    expect(String(url)).toContain('probe=')
    expect(init).toMatchObject({ mode: 'no-cors', cache: 'no-store' })
  })

  it('classifies a rejection by how long it took', async () => {
    const fetchImpl = vi.fn().mockRejectedValue(new TypeError('Failed to fetch'))
    const fast = await probeHost('SIN-01', 'assets-abc.example.com', {
      fetchImpl: fetchImpl as unknown as typeof fetch,
      now: clock(0, 420),
    })
    expect(fast.outcome).toBe('tls-refused')

    const slow = await probeHost('SIN-01', 'assets-abc.example.com', {
      fetchImpl: fetchImpl as unknown as typeof fetch,
      now: clock(0, 6000),
    })
    expect(slow.outcome).toBe('blocked')
  })

  it('gives up at the timeout instead of hanging', async () => {
    // The probe must abort on its own; a never-settling fetch would otherwise
    // leave the row spinning forever.
    const fetchImpl = vi.fn(
      (_url: string, init?: RequestInit) =>
        new Promise((_resolve, reject) => {
          init?.signal?.addEventListener('abort', () => reject(new Error('aborted')))
        }),
    )
    const result = await probeHost('X', 'h.example.com', {
      fetchImpl: fetchImpl as unknown as typeof fetch,
      now: clock(0, 50),
      timeoutMs: 10,
    })
    expect(result.outcome).toBe('tls-refused')
    expect(fetchImpl).toHaveBeenCalledOnce()
  })
})
