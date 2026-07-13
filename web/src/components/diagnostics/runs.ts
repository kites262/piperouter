import { reactive } from 'vue'

import type { DiagnosticsResult } from '@/api/types'

/**
 * request  — Diagnostics page: inbound path match (data-plane simulation)
 * route    — Routes page named-route test
 * transport — Transports page connectivity test
 */
export type DiagnosticsKind = 'request' | 'route' | 'transport'

/** One diagnostics execution, kept in the session-local history list. */
export interface DiagnosticsRun {
  id: number
  kind: DiagnosticsKind
  /**
   * Matched/named route (request/route) or transport name (transport).
   * For request probes this is filled from the result after matching.
   */
  subject: string
  /** Full path (request), extra path (route), or test URL (transport). */
  input: string
  method: string
  /** Completion time, Unix milliseconds — always rendered locally. */
  at: number
  result: DiagnosticsResult
}

/** Badge variant for a run's HTTP status / outcome. */
export type ResultTone = 'success' | 'accent' | 'warning' | 'danger' | 'muted'

/** Tone for an HTTP status code (0 = no response). */
export function statusTone(status: number): ResultTone {
  if (status >= 200 && status < 300) return 'success'
  if (status >= 300 && status < 400) return 'accent'
  if (status >= 400 && status < 500) return 'warning'
  if (status >= 500) return 'danger'
  return 'muted'
}

/** Format a millisecond duration for display, e.g. "12.3 ms" / "1.52 s". */
export function formatMs(ms: number): string {
  if (!Number.isFinite(ms) || ms < 0) return '—'
  if (ms >= 1000) return `${(ms / 1000).toFixed(2)} s`
  if (ms >= 100) return `${ms.toFixed(0)} ms`
  return `${ms.toFixed(1)} ms`
}

// ---------------------------------------------------------------------------
// Session-local run history (module scope — survives page navigation, lost on
// reload; diagnostics results are intentionally never persisted, PRD §16).
// ---------------------------------------------------------------------------

const MAX_RUNS = 25

const history = reactive<DiagnosticsRun[]>([])
let seq = 0

export function useRunHistory() {
  function add(run: Omit<DiagnosticsRun, 'id' | 'at'>): DiagnosticsRun {
    const full: DiagnosticsRun = { ...run, id: ++seq, at: Date.now() }
    history.unshift(full)
    if (history.length > MAX_RUNS) history.splice(MAX_RUNS)
    return full
  }

  function clear(): void {
    history.splice(0)
  }

  return { runs: history, add, clear }
}
