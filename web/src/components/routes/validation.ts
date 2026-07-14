/**
 * Route form validation + rewrite preview — mirrors backend rules exactly
 * (mirrors the server-side config validation in internal/config + internal/router).
 */
import type { RouteConfig, TransportConfig } from '@/api/types'

/** Route/transport name constraint (config.NamePattern). */
export const NAME_RE = /^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$/

/**
 * §6.4 prefix normalization: trim whitespace and remove trailing "/" from
 * non-root prefixes ("/openai/" → "/openai", "/" stays "/").
 */
export function normalizePrefix(prefix: string): string {
  let p = prefix.trim()
  while (p.length > 1 && p.endsWith('/')) p = p.slice(0, -1)
  return p
}

export function validateName(
  name: string,
  routes: RouteConfig[],
  originalName: string | null,
): string | null {
  if (name === '') return 'Name is required.'
  if (!NAME_RE.test(name)) {
    return 'Must start with a letter or digit and contain only letters, digits, ".", "_", "-" (max 64 chars).'
  }
  if (routes.some((r) => r.name === name && r.name !== originalName)) {
    return `A route named "${name}" already exists.`
  }
  return null
}

/** Format-only prefix check (no uniqueness) — also used by the preview. */
export function prefixSyntaxError(prefix: string): string | null {
  if (prefix === '') return 'Prefix is required.'
  if (!prefix.startsWith('/')) return 'Prefix must start with "/".'
  if (prefix.includes('?') || prefix.includes('#')) return 'Prefix must not contain "?" or "#".'
  if (prefix !== '/' && prefix.endsWith('/')) {
    return 'Non-root prefix must not end with "/" (trimmed automatically on blur).'
  }
  if (prefix.includes('//')) return 'Prefix must not contain empty "//" segments.'
  if (prefix.split('/').includes('..')) return 'Prefix must not contain ".." segments.'
  return null
}

export function validatePrefix(
  prefix: string,
  routes: RouteConfig[],
  originalName: string | null,
): string | null {
  const syntax = prefixSyntaxError(prefix)
  if (syntax !== null) return syntax
  if (routes.some((r) => r.prefix === prefix && r.name !== originalName)) {
    const owner = routes.find((r) => r.prefix === prefix && r.name !== originalName)
    return `Prefix "${prefix}" is already used by route "${owner?.name ?? ''}".`
  }
  return null
}

export function validateTarget(target: string, type: 'proxy' | 'static' = 'proxy'): string | null {
  if (type === 'static') return validateStaticTarget(target)
  if (target === '') return 'Target is required.'
  if (target.includes('?')) return 'Target must not contain a query string.'
  if (target.includes('#')) return 'Target must not contain a fragment.'
  let url: URL
  try {
    url = new URL(target)
  } catch {
    return 'Target must be an absolute URL, e.g. https://api.example.com/v1.'
  }
  if (url.protocol !== 'http:' && url.protocol !== 'https:') {
    return 'Target scheme must be http or https.'
  }
  if (url.username !== '' || url.password !== '') return 'Target must not contain userinfo.'
  if (url.hostname === '') return 'Target must include a host.'
  return null
}

/**
 * Filesystem path to a single file (mirrors config.ResolveStaticFilePath).
 * Absolute, or relative to the configuration file's directory on the server.
 * The UI cannot resolve the server baseDir; relative paths are accepted here
 * and validated fully when the server applies the config.
 */
export function validateStaticTarget(target: string): string | null {
  if (target === '') return 'File path is required.'
  if (target.includes('://')) return 'Use a filesystem path, not a URL (no file://).'
  if (target.endsWith('/') || target.endsWith('\\')) {
    return 'Must be a file path, not a directory (no trailing separator).'
  }
  // ".." is allowed (resolved against the config file directory on the server;
  // may leave that directory). Absolute paths already have the same reach.
  return null
}

export function validateTransport(name: string, transports: TransportConfig[]): string | null {
  if (name === '') return 'Transport is required.'
  if (name === 'direct') return null
  if (!transports.some((t) => t.name === name)) return `Transport "${name}" does not exist.`
  return null
}

export interface PreviewMapping {
  from: string
  to: string
}

/**
 * Example request mapping for the editor preview (PRD §19.3), following the
 * rewrite semantics of internal/router (§8): base = target path with trailing
 * "/" trimmed; rest = path minus prefix when strip_prefix (root "/" strips
 * nothing); final = base + rest, or "/" when empty.
 */
export function previewMapping(
  prefixInput: string,
  target: string,
  stripPrefix: boolean,
  type: 'proxy' | 'static' = 'proxy',
  match: 'prefix' | 'exact' = 'prefix',
): PreviewMapping | null {
  const prefix = normalizePrefix(prefixInput)
  if (prefixSyntaxError(prefix) !== null) return null
  if (validateTarget(target.trim(), type) !== null) return null

  // Exact routes only ever see their literal prefix, so that IS the example.
  const literalOnly = type === 'static' || match === 'exact'
  const example = literalOnly ? (prefix === '/' ? '/' : prefix) : '/chat/completions'
  const from = literalOnly ? example : prefix === '/' ? example : `${prefix}${example}`

  if (type === 'static') {
    return { from, to: `file ${target.trim()}` }
  }

  const url = new URL(target.trim())
  // Mirror internal/router: base := target path with trailing "/" trimmed once.
  let base = url.pathname
  if (base.endsWith('/')) base = base.slice(0, -1)
  const rest = stripPrefix && prefix !== '/' ? from.slice(prefix.length) : from
  let final = `${base}${rest}`
  if (final === '') final = '/'

  return { from, to: `${url.protocol}//${url.host}${final}` }
}
