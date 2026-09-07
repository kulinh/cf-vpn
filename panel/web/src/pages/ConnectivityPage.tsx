import { useEffect, useState } from 'react'
import { ErrorBanner } from '../components/ui/ErrorBanner'
import { listNodes } from '../lib/api'
import { describeLoadError } from '../lib/errors'
import {
  PROBE_TIMEOUT_MS,
  describeOutcome,
  probeHost,
  type ProbeResult,
} from '../lib/connectivity'
import type { Node } from '../lib/types'

const OUTCOME_STYLE: Record<ProbeResult['outcome'], string> = {
  reachable: 'bg-emerald-100 text-emerald-900 dark:bg-emerald-900/40 dark:text-emerald-200',
  'tls-refused': 'bg-emerald-100 text-emerald-900 dark:bg-emerald-900/40 dark:text-emerald-200',
  blocked: 'bg-rose-100 text-rose-900 dark:bg-rose-900/40 dark:text-rose-200',
  'not-attempted': 'bg-amber-100 text-amber-900 dark:bg-amber-900/40 dark:text-amber-200',
}

const OUTCOME_LABEL: Record<ProbeResult['outcome'], string> = {
  reachable: 'Reachable',
  'tls-refused': 'Reachable',
  blocked: 'Blocked',
  'not-attempted': 'Not measured',
}

export function ConnectivityPage() {
  const [nodes, setNodes] = useState<Node[]>([])
  const [results, setResults] = useState<Record<string, ProbeResult>>({})
  const [running, setRunning] = useState(false)
  const [loadError, setLoadError] = useState<string | null>(null)

  useEffect(() => {
    let mounted = true
    listNodes()
      .then((items) => {
        if (mounted) setNodes(items.filter((node) => node.vpnHost))
      })
      .catch((error: unknown) => {
        if (mounted) setLoadError(describeLoadError(error))
      })
    return () => {
      mounted = false
    }
  }, [])

  const runAll = async () => {
    setRunning(true)
    setResults({})
    // Sequential on purpose: parallel probes share the radio and the DNS
    // resolver, and the elapsed time is the measurement here, not a detail.
    for (const node of nodes) {
      const result = await probeHost(node.id, node.vpnHost, {
        fetchImpl: fetch.bind(globalThis),
        now: () => performance.now(),
      })
      setResults((current) => ({ ...current, [node.id]: result }))
    }
    setRunning(false)
  }

  return (
    <section className="space-y-4">
      <div className="flex flex-wrap items-center gap-3">
        <h1 className="text-lg font-semibold">Connectivity</h1>
        <button
          type="button"
          onClick={runAll}
          disabled={running}
          className="rounded bg-slate-900 px-3 py-1.5 text-sm text-white disabled:opacity-50 dark:bg-slate-100 dark:text-slate-900"
        >
          {running ? 'Testing…' : 'Run test'}
        </button>
      </div>

      <div className="rounded border border-amber-300 bg-amber-50 p-3 text-sm text-amber-900 dark:border-amber-700 dark:bg-amber-950/40 dark:text-amber-200">
        <p className="font-medium">This measures TCP + TLS from this device, on this network.</p>
        <ul className="mt-1 list-disc space-y-0.5 pl-5">
          <li>
            <strong>It says nothing about Hysteria2.</strong> HY2 runs over UDP, which a browser
            cannot send. Only the VPN client can test that.
          </li>
          <li>
            A direct-mode node runs Reality and will decline the handshake — that still proves the
            endpoint is reachable, and is reported as such.
          </li>
          <li>
            Reachable does not mean usable: wrong credentials also present as a timeout in the
            client. Run <code>scripts/check-fleet-drift.sh</code> to rule that out.
          </li>
        </ul>
      </div>

      {loadError ? <ErrorBanner message={loadError} /> : null}

      <table className="w-full border-collapse text-sm">
        <thead>
          <tr className="border-b border-slate-200 text-left dark:border-slate-800">
            <th className="py-2 pr-3">Node</th>
            <th className="py-2 pr-3">Endpoint</th>
            <th className="py-2 pr-3">Mode</th>
            <th className="py-2 pr-3">Result</th>
            <th className="py-2 pr-3">Time</th>
          </tr>
        </thead>
        <tbody>
          {nodes.map((node) => {
            const result = results[node.id]
            return (
              <tr key={node.id} className="border-b border-slate-100 dark:border-slate-900">
                <td className="py-2 pr-3 font-medium">{node.label || node.id}</td>
                <td className="py-2 pr-3 font-mono text-xs break-all">{node.vpnHost}</td>
                <td className="py-2 pr-3">{node.mode ?? '—'}</td>
                <td className="py-2 pr-3">
                  {result ? (
                    <span
                      title={describeOutcome(result.outcome)}
                      className={`inline-block rounded px-2 py-0.5 text-xs ${OUTCOME_STYLE[result.outcome]}`}
                    >
                      {OUTCOME_LABEL[result.outcome]}
                    </span>
                  ) : (
                    <span className="text-slate-400">—</span>
                  )}
                </td>
                <td className="py-2 pr-3 tabular-nums">{result ? `${result.elapsedMs} ms` : '—'}</td>
              </tr>
            )
          })}
        </tbody>
      </table>

      {nodes.length === 0 && !loadError ? (
        <p className="text-sm text-slate-500">
          No nodes with a VPN endpoint to test. The node list comes from the panel API — if you are
          signed in and this stays empty, the API call failed rather than returning nothing.
        </p>
      ) : null}

      <p className="text-xs text-slate-500">
        Each probe gives up after {PROBE_TIMEOUT_MS / 1000}s. A blocked result means no answer
        arrived in that window — the path is dropped, or the host does not resolve here.
      </p>
    </section>
  )
}
