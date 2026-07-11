/**
 * Dashboard-local formatting helpers (PRD §19.1).
 * All timestamps are rendered in the viewer's local timezone.
 */

/** "3d 4h", "2h 15m", "5m 12s", "42s". */
export function formatUptime(totalSeconds: number): string {
  if (!Number.isFinite(totalSeconds) || totalSeconds < 0) return '—'
  const s = Math.floor(totalSeconds)
  const days = Math.floor(s / 86400)
  const hours = Math.floor((s % 86400) / 3600)
  const minutes = Math.floor((s % 3600) / 60)
  const seconds = s % 60
  if (days > 0) return `${days}d ${hours}h`
  if (hours > 0) return `${hours}h ${minutes}m`
  if (minutes > 0) return `${minutes}m ${seconds}s`
  return `${seconds}s`
}

/** Human latency: "<1 ms", "42 ms", "1.24 s". */
export function formatMs(ms: number): string {
  if (!Number.isFinite(ms) || ms < 0) return '—'
  if (ms === 0) return '0 ms'
  if (ms < 1) return '<1 ms'
  if (ms < 1000) return `${Math.round(ms)} ms`
  const s = ms / 1000
  return `${s >= 10 ? s.toFixed(1) : s.toFixed(2)} s`
}

const compactFormat = new Intl.NumberFormat(undefined, {
  notation: 'compact',
  maximumFractionDigits: 1,
})

/** Locale count; compact ("12.3K") from 10 000 up. */
export function formatCount(n: number): string {
  if (!Number.isFinite(n) || n < 0) return '—'
  return n < 10000 ? n.toLocaleString() : compactFormat.format(n)
}

/** Error percentage, div0-guarded: total <= 0 → "—". */
export function formatErrorRate(errors: number, total: number): string {
  if (!Number.isFinite(errors) || !Number.isFinite(total) || total <= 0) return '—'
  const pct = (errors / total) * 100
  if (pct === 0) return '0%'
  if (pct < 0.1) return '<0.1%'
  return `${pct >= 10 ? pct.toFixed(0) : pct.toFixed(1)}%`
}

/** "just now", "12s ago", "3m ago", "2h ago", "5d ago"; null → "never". */
export function formatRelativeTime(iso: string | null | undefined): string {
  if (iso === null || iso === undefined || iso === '') return 'never'
  const t = new Date(iso).getTime()
  if (Number.isNaN(t)) return '—'
  const s = Math.floor((Date.now() - t) / 1000)
  if (s < 5) return 'just now'
  if (s < 60) return `${s}s ago`
  const m = Math.floor(s / 60)
  if (m < 60) return `${m}m ago`
  const h = Math.floor(m / 60)
  if (h < 24) return `${h}h ago`
  return `${Math.floor(h / 24)}d ago`
}

/** Local wall-clock time, e.g. "14:03:27". */
export function formatLocalTime(iso: string): string {
  const d = new Date(iso)
  return Number.isNaN(d.getTime()) ? iso : d.toLocaleTimeString(undefined, { hour12: false })
}

/** Full local date + time (tooltips). */
export function formatLocalDateTime(iso: string): string {
  const d = new Date(iso)
  return Number.isNaN(d.getTime()) ? iso : d.toLocaleString()
}

/** "sha256:<hex>" → first 12 hex chars. */
export function shortRevision(revision: string): string {
  if (revision === '') return ''
  return revision.startsWith('sha256:') ? revision.slice(7, 19) : revision.slice(0, 12)
}
