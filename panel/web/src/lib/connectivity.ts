/**
 * Browser-side reachability probe: does *this* device, on *this* network, reach
 * a node's VPN endpoint?
 *
 * Every check the fleet already has runs server-side — from a VPS with clean
 * routing — so it cannot answer the question a user actually asks when a node
 * times out in their client. Only code on the device can.
 *
 * What a browser can measure, and what it cannot:
 *
 *  - It cannot open a raw TCP or UDP socket, so Hysteria2 (UDP/QUIC) is out of
 *    reach entirely. Nothing here says anything about HY2.
 *  - `fetch` is the only lever, and it needs a TLS handshake the browser
 *    accepts. Cloudflare-mode nodes present a real certificate and answer 404,
 *    so the request resolves. Direct-mode nodes run Reality: a handshake whose
 *    SNI is not one of `serverNames` is forwarded to the camouflage host, so
 *    the browser is offered a certificate for a different name and rejects it.
 *
 * That rejection is not a failure to reach the node — TCP connected and the
 * server answered. Measured from a VPS: direct-mode nodes reject in 0.3-0.65s
 * while an unroutable address runs to the full timeout. Elapsed time is
 * therefore the signal, not success.
 */

export type ProbeOutcome = 'reachable' | 'tls-refused' | 'blocked'

export type ProbeResult = {
  nodeId: string
  host: string
  outcome: ProbeOutcome
  elapsedMs: number
}

export const PROBE_TIMEOUT_MS = 6000

/**
 * A rejection slower than this is treated as a dead path rather than a refused
 * handshake. Well clear of the 0.65s worst case measured across the fleet, and
 * well under PROBE_TIMEOUT_MS, so neither bound is near the decision point.
 */
export const FAST_FAIL_MS = 3000

/**
 * classifyProbe maps one attempt onto an outcome.
 *
 * `resolved` means the browser completed a request, which requires both TCP and
 * a certificate it trusts. A rejection under FAST_FAIL_MS means the connection
 * got far enough to be refused — the node is reachable and the handshake was
 * declined, which is the expected shape for Reality. Anything slower is
 * reported as blocked.
 */
export function classifyProbe(resolved: boolean, elapsedMs: number): ProbeOutcome {
  if (resolved) return 'reachable'
  return elapsedMs < FAST_FAIL_MS ? 'tls-refused' : 'blocked'
}

export function describeOutcome(outcome: ProbeOutcome): string {
  switch (outcome) {
    case 'reachable':
      return 'Reachable — TCP and TLS both completed.'
    case 'tls-refused':
      return 'Reachable — TCP connected, the server declined the handshake. Expected for a Reality node; the endpoint is not blocked on this network.'
    case 'blocked':
      return 'No answer before the timeout — blocked, dropped, or the host does not resolve on this network.'
  }
}

export type ProbeDeps = {
  fetchImpl: typeof fetch
  now: () => number
  timeoutMs?: number
}

/**
 * probeHost issues one no-cors GET and reports how it ended. The response body
 * is meaningless (opaque), and deliberately so: the only facts wanted here are
 * "did it complete" and "how long did that take".
 *
 * A cache-busting query keeps a repeat run from being answered by the HTTP
 * cache, which would report a path that is no longer open as reachable.
 */
export async function probeHost(
  nodeId: string,
  host: string,
  deps: ProbeDeps,
): Promise<ProbeResult> {
  const timeoutMs = deps.timeoutMs ?? PROBE_TIMEOUT_MS
  const controller = new AbortController()
  const timer = setTimeout(() => controller.abort(), timeoutMs)
  const started = deps.now()
  let resolved = false
  try {
    await deps.fetchImpl(`https://${host}/?probe=${started}`, {
      mode: 'no-cors',
      cache: 'no-store',
      redirect: 'manual',
      signal: controller.signal,
    })
    resolved = true
  } catch {
    resolved = false
  } finally {
    clearTimeout(timer)
  }
  const elapsedMs = Math.round(deps.now() - started)
  return { nodeId, host, outcome: classifyProbe(resolved, elapsedMs), elapsedMs }
}
