/**
 * PipeRouter Admin API client — thin fetch wrapper over /api/v1.
 *
 * Contract: api/openapi.yaml (the Admin API spec). All request/response
 * bodies are JSON; error responses carry {error, detail?, issues?} and are
 * surfaced as thrown ApiError instances.
 */
import type {
  ApiErrorBody,
  Config,
  ConfigEnvelope,
  ConfigUpdateRequest,
  DiagnosticsResult,
  LogsQuery,
  LogsResponse,
  MetricsHistoryResponse,
  MetricsSnapshot,
  RouteConfig,
  RouteMetrics,
  RequestTestRequest,
  RouteTestRequest,
  RoutesResponse,
  StatusResponse,
  TransportConfig,
  TransportTestRequest,
  TransportsResponse,
  ValidateResponse,
} from './types'

const BASE = '/api/v1'

/** Thrown for any non-2xx response (and for network failures, status = 0). */
export class ApiError extends Error {
  /** HTTP status code; 0 when the request never reached the server. */
  readonly status: number
  /** Machine-readable error code, e.g. "validation_failed". */
  readonly code: string
  /** Human-readable detail from the server ("" if none). */
  readonly detail: string
  /** Validation issues (only for validation_failed). */
  readonly issues: string[]

  constructor(status: number, code: string, detail = '', issues: string[] = []) {
    super(detail !== '' ? detail : code)
    this.name = 'ApiError'
    this.status = status
    this.code = code
    this.detail = detail
    this.issues = issues
  }
}

async function request<T>(method: string, path: string, body?: unknown): Promise<T> {
  let res: Response
  try {
    res = await fetch(path, {
      method,
      headers:
        body !== undefined
          ? { Accept: 'application/json', 'Content-Type': 'application/json' }
          : { Accept: 'application/json' },
      body: body !== undefined ? JSON.stringify(body) : undefined,
    })
  } catch (err) {
    throw new ApiError(0, 'network_error', err instanceof Error ? err.message : 'network request failed')
  }

  const text = await res.text()
  let data: unknown
  if (text !== '') {
    try {
      data = JSON.parse(text)
    } catch {
      data = undefined
    }
  }

  if (!res.ok) {
    const errBody = (data ?? {}) as Partial<ApiErrorBody>
    throw new ApiError(
      res.status,
      typeof errBody.error === 'string' && errBody.error !== '' ? errBody.error : `http_${res.status}`,
      typeof errBody.detail === 'string' ? errBody.detail : '',
      Array.isArray(errBody.issues) ? errBody.issues.filter((i): i is string => typeof i === 'string') : [],
    )
  }

  return data as T
}

/** Unwrap `{key: value}` envelopes but tolerate bare objects. */
function unwrap<T>(data: unknown, key: string): T {
  if (data !== null && typeof data === 'object' && key in (data as Record<string, unknown>)) {
    const value = (data as Record<string, unknown>)[key]
    if (value !== null && value !== undefined) return value as T
  }
  return data as T
}

// ---------------------------------------------------------------------------
// Status & config
// ---------------------------------------------------------------------------

export function getStatus(): Promise<StatusResponse> {
  return request('GET', `${BASE}/status`)
}

export function getConfig(): Promise<ConfigEnvelope> {
  return request('GET', `${BASE}/config`)
}

/** Replace the full configuration. Pass envelope.revision for conflict detection (409 revision_conflict). */
export async function putConfig(envelope: ConfigUpdateRequest): Promise<void> {
  await request('PUT', `${BASE}/config`, envelope)
}

/** Validate only — never saves or applies; invalid content still resolves (valid=false). */
export function validateConfig(config: Config): Promise<ValidateResponse> {
  return request('POST', `${BASE}/config/validate`, { config })
}

// ---------------------------------------------------------------------------
// Routes
// ---------------------------------------------------------------------------

export async function listRoutes(): Promise<RouteConfig[]> {
  const data = await request<RoutesResponse>('GET', `${BASE}/routes`)
  return data.routes ?? []
}

export async function createRoute(route: RouteConfig, revision?: string): Promise<void> {
  await request('POST', `${BASE}/routes`, { revision, route })
}

export async function getRoute(name: string): Promise<RouteConfig> {
  return unwrap<RouteConfig>(await request('GET', `${BASE}/routes/${encodeURIComponent(name)}`), 'route')
}

export async function updateRoute(name: string, route: RouteConfig, revision?: string): Promise<void> {
  await request('PUT', `${BASE}/routes/${encodeURIComponent(name)}`, { revision, route })
}

export async function deleteRoute(name: string, revision?: string): Promise<void> {
  await request('DELETE', `${BASE}/routes/${encodeURIComponent(name)}`, { revision })
}

// ---------------------------------------------------------------------------
// Transports
// ---------------------------------------------------------------------------

export async function listTransports(): Promise<TransportConfig[]> {
  const data = await request<TransportsResponse>('GET', `${BASE}/transports`)
  return data.transports ?? []
}

export async function createTransport(transport: TransportConfig, revision?: string): Promise<void> {
  await request('POST', `${BASE}/transports`, { revision, transport })
}

export async function getTransport(name: string): Promise<TransportConfig> {
  return unwrap<TransportConfig>(
    await request('GET', `${BASE}/transports/${encodeURIComponent(name)}`),
    'transport',
  )
}

export async function updateTransport(
  name: string,
  transport: TransportConfig,
  revision?: string,
): Promise<void> {
  await request('PUT', `${BASE}/transports/${encodeURIComponent(name)}`, { revision, transport })
}

export async function deleteTransport(name: string, revision?: string): Promise<void> {
  await request('DELETE', `${BASE}/transports/${encodeURIComponent(name)}`, { revision })
}

// ---------------------------------------------------------------------------
// Metrics & logs
// ---------------------------------------------------------------------------

export function getMetrics(): Promise<MetricsSnapshot> {
  return request('GET', `${BASE}/metrics`)
}

export function getMetricsHistory(): Promise<MetricsHistoryResponse> {
  return request('GET', `${BASE}/metrics/history`)
}

export async function getRouteMetrics(name: string): Promise<RouteMetrics> {
  return unwrap<RouteMetrics>(
    await request('GET', `${BASE}/routes/${encodeURIComponent(name)}/metrics`),
    'metrics',
  )
}

export function getLogs(query: LogsQuery = {}): Promise<LogsResponse> {
  const params = new URLSearchParams()
  if (query.limit !== undefined) params.set('limit', String(query.limit))
  if (query.route !== undefined && query.route !== '') params.set('route', query.route)
  if (query.status_class !== undefined && query.status_class.length > 0) {
    params.set('status_class', query.status_class)
  }
  const qs = params.toString()
  return request('GET', `${BASE}/logs${qs !== '' ? `?${qs}` : ''}`)
}

// ---------------------------------------------------------------------------
// Diagnostics
// ---------------------------------------------------------------------------

/** Simulate an inbound data-plane request (path match → rewrite → probe). */
export function testRequest(req: RequestTestRequest): Promise<DiagnosticsResult> {
  return request('POST', `${BASE}/diagnostics/request`, req)
}

/** Probe a named route end-to-end (Routes page Test). */
export function testRoute(req: RouteTestRequest): Promise<DiagnosticsResult> {
  return request('POST', `${BASE}/diagnostics/route`, req)
}

/** Probe a URL through a named transport (Transports page Test). */
export function testTransport(req: TransportTestRequest): Promise<DiagnosticsResult> {
  return request('POST', `${BASE}/diagnostics/transport`, req)
}
