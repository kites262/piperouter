/**
 * Transports page — shared page-local helpers and types.
 *
 * Validation mirrors internal/config/validate.go so users get instant
 * feedback; the server remains the source of truth (validation_failed).
 */
import type { DiagnosticStage, TransportType } from '@/api/types'

/** Mirrors config.NamePattern (internal/config/types.go). */
export const NAME_RE = /^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$/

/** Session-local record of the last diagnostics run for one transport. */
export interface TransportTestRecord {
  ok: boolean
  status: number
  errorStage: DiagnosticStage
  error: string
  headerDurationMs: number
  totalDurationMs: number
  url: string
  method: string
  /** Epoch milliseconds — always rendered with local formatting. */
  testedAt: number
}

function parseUrl(raw: string): URL | null {
  try {
    return new URL(raw)
  } catch {
    return null
  }
}

/** Client-side mirror of the server's transport-name rules. */
export function validateTransportName(name: string, takenNames: readonly string[]): string | null {
  if (name === '') return 'Name is required.'
  if (name === 'direct') return '"direct" is reserved for the built-in transport.'
  if (!NAME_RE.test(name)) {
    return 'Must start with a letter or digit and contain only letters, digits, ".", "_" or "-" (max 64 characters).'
  }
  if (takenNames.includes(name)) return `A transport named "${name}" already exists.`
  return null
}

/** Proxy URL rules: scheme must match type, no userinfo, non-empty host. */
export function validateProxyUrl(raw: string, type: string): string | null {
  if (raw === '') return 'Proxy URL is required.'
  const u = parseUrl(raw)
  if (u === null) return 'Not a valid URL.'
  const want = type === 'socks5' ? 'socks5:' : 'http:'
  if (u.protocol !== want) return `URL scheme must be ${want}// for type "${type}".`
  if (u.username !== '' || u.password !== '') {
    return 'URL must not contain userinfo — proxy credentials are not supported.'
  }
  if (u.hostname === '') return 'URL host must not be empty.'
  return null
}

/** Diagnostics test URL: http/https only (server answers 400 invalid_request otherwise). */
export function validateTestUrl(raw: string): string | null {
  if (raw === '') return 'Test URL is required.'
  const u = parseUrl(raw)
  if (u === null) return 'Not a valid URL.'
  if (u.protocol !== 'http:' && u.protocol !== 'https:') {
    return 'Test URL scheme must be http or https.'
  }
  return null
}

export function typeBadgeVariant(type: TransportType): 'accent' | 'default' | 'muted' {
  if (type === 'direct') return 'muted'
  if (type === 'http') return 'accent'
  return 'default'
}

export function formatMs(ms: number): string {
  if (!Number.isFinite(ms) || ms < 0) return '—'
  if (ms >= 1000) return `${(ms / 1000).toFixed(2)} s`
  if (ms >= 100) return `${Math.round(ms)} ms`
  return `${ms.toFixed(1)} ms`
}

export function truncate(value: string, max = 48): string {
  return value.length <= max ? value : `${value.slice(0, max - 1)}…`
}
