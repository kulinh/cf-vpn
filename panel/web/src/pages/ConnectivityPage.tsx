import { useEffect, useState } from 'react'
import { ErrorBanner } from '../components/ui/ErrorBanner'
import { listNodes } from '../lib/api'
import { describeLoadError } from '../lib/errors'
import {
  PROBE_TIMEOUT_MS,
  describeOutcome,
  probeHost,
  sortByResult,
  type ProbeResult,
} from '../lib/connectivity'
import type { Node } from '../lib/types'
import { ModeBadge, PageHeader, btnMd, btnPrimary, muted, tableWrap, td, th, thead, tr } from '../components/ui/theme'

const OUTCOME_STYLE: Record<ProbeResult['outcome'], string> = {
  reachable: 'bg-emerald-50 text-emerald-700 ring-emerald-200 dark:bg-emerald-500/10 dark:text-emerald-300 dark:ring-emerald-500/30',
  'tls-refused': 'bg-emerald-50 text-emerald-700 ring-emerald-200 dark:bg-emerald-500/10 dark:text-emerald-300 dark:ring-emerald-500/30',
  blocked: 'bg-rose-50 text-rose-700 ring-rose-200 dark:bg-rose-500/10 dark:text-rose-300 dark:ring-rose-500/30',
  'not-attempted': 'bg-amber-50 text-amber-800 ring-amber-200 dark:bg-amber-500/10 dark:text-amber-300 dark:ring-amber-500/30',
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

  // Only reorder once the run is over: rows jumping while each probe lands
  // makes the table unreadable and moves the row you are watching.
  const displayNodes = running ? nodes : sortByResult(nodes, results)

  return (
    <section className="space-y-4">
      <PageHeader
        title="Connectivity"
        subtitle="Probe each node's VPN endpoint from this browser."
        actions={
          <button type="button" onClick={runAll} disabled={running} className={`${btnPrimary} ${btnMd}`}>
            {running ? 'Testing…' : 'Run test'}
          </button>
        }
      />

      {loadError ? <ErrorBanner message={loadError} /> : null}

      <div className={tableWrap}>
        <table className="w-full border-collapse text-sm">
          <thead className={thead}>
            <tr>
              <th className={th}>Node</th>
              <th className={th}>Endpoint</th>
              <th className={th}>Mode</th>
              <th className={th}>Result</th>
              <th className={th}>Time</th>
            </tr>
          </thead>
          <tbody>
            {displayNodes.map((node) => {
              const result = results[node.id]
              return (
                <tr key={node.id} className={tr}>
                  <td className={`${td} font-medium text-slate-900 dark:text-slate-100`}>{node.label || node.id}</td>
                  <td className={`${td} break-all font-mono text-xs text-slate-600 dark:text-slate-400`}>{node.vpnHost}</td>
                  <td className={td}>{node.mode ? <ModeBadge mode={node.mode} /> : <span className={muted}>—</span>}</td>
                  <td className={td}>
                    {result ? (
                      <span
                        title={describeOutcome(result.outcome)}
                        className={`inline-block rounded-full px-2 py-0.5 text-xs font-medium ring-1 ring-inset ${OUTCOME_STYLE[result.outcome]}`}
                      >
                        {OUTCOME_LABEL[result.outcome]}
                      </span>
                    ) : (
                      <span className={muted}>—</span>
                    )}
                  </td>
                  <td className={`${td} tabular-nums text-slate-600 dark:text-slate-300`}>{result ? `${result.elapsedMs} ms` : '—'}</td>
                </tr>
              )
            })}
          </tbody>
        </table>
      </div>

      {nodes.length === 0 && !loadError ? (
        <p className="text-sm text-slate-500 dark:text-slate-400">
          No nodes with a VPN endpoint to test. The node list comes from the panel API — if you are
          signed in and this stays empty, the API call failed rather than returning nothing.
        </p>
      ) : null}

      <p className="text-xs text-slate-500 dark:text-slate-400">
        Each probe gives up after {PROBE_TIMEOUT_MS / 1000}s. A blocked result means no answer
        arrived in that window — the path is dropped, or the host does not resolve here.
      </p>
    </section>
  )
}
