/**
 * Hand-written TypeScript mirrors of the PipeRouter Admin API contract.
 *
 * Source of truth: api/openapi.yaml (the Admin API contract) and
 * internal/config/types.go (JSON tags). All field names are snake_case,
 * exactly as the Go backend emits them. Do not rename fields.
 */

/** Go `config.Duration` marshals to a Go duration string, e.g. "10s", "1m30s". */
export type DurationString = string

/** RFC 3339 timestamp string as emitted by Go's time.Time JSON marshalling. */
export type TimeString = string

export type TransportType = 'direct' | 'http' | 'socks5'

export type LogLevel = 'debug' | 'info' | 'warn' | 'error'

/** Streaming classification of a proxied request. */
export type StreamKind = '' | 'sse' | 'websocket'

/** Log filter: status class or "error" (entries with a non-empty error code). */
export type StatusClass = '2xx' | '3xx' | '4xx' | '5xx' | 'error'

/** Diagnostics failure stage ("" = no error). */
export type DiagnosticStage = '' | 'resolve' | 'connect' | 'tls' | 'response'

// ---------------------------------------------------------------------------
// Configuration (internal/config/types.go)
// ---------------------------------------------------------------------------

export interface TLSConfig {
  enabled: boolean
  cert_file: string
  key_file: string
}

export interface ProxyServerConfig {
  listen: string
  tls: TLSConfig
}

export interface AdminServerConfig {
  enabled: boolean
  listen: string
}

export interface WebConfig {
  enabled: boolean
}

export interface ServerConfig {
  proxy: ProxyServerConfig
  admin: AdminServerConfig
  web: WebConfig
}

export interface RuntimeConfig {
  log_level: LogLevel
  recent_logs: number
}

export interface NetworkConfig {
  dial_timeout: DurationString
  tls_handshake_timeout: DurationString
  response_header_timeout: DurationString
  idle_connection_timeout: DurationString
}

/** Outbound proxy link. The built-in `direct` transport is synthetic: {name:"direct",type:"direct",url:""}. */
export interface TransportConfig {
  name: string
  type: TransportType
  url: string
}

/** Route handler mode. */
export type RouteType = 'proxy' | 'static'

/** One prefix → backend mapping. */
export interface RouteConfig {
  name: string
  enabled: boolean
  /** "proxy" (default reverse-proxy) or "static" (single local file). */
  type: RouteType
  prefix: string
  /**
   * Proxy: absolute http(s) URL. Static: filesystem path to a regular file
   * (absolute, or relative to the config file's directory on the server).
   */
  target: string
  /** Remove Forwarded/Via/X-Forwarded-* before forwarding (default true). Ignored for static. */
  strip_forward_headers: boolean
  strip_prefix: boolean
  transport: string
}

/** Root configuration object (always returned normalized by the API). */
export interface Config {
  version: number
  server: ServerConfig
  runtime: RuntimeConfig
  network: NetworkConfig
  transports: TransportConfig[]
  routes: RouteConfig[]
}

// ---------------------------------------------------------------------------
// GET /api/v1/status
// ---------------------------------------------------------------------------

export interface StatusConfigInfo {
  valid: boolean
  last_error: string
  revision: string
  loaded_at: TimeString
  path: string
}

export interface StatusResponse {
  version: string
  started_at: TimeString
  uptime_seconds: number
  proxy: {
    listen: string
    tls_enabled: boolean
  }
  admin: {
    listen: string
  }
  config: StatusConfigInfo
}

// ---------------------------------------------------------------------------
// GET/PUT /api/v1/config, POST /api/v1/config/validate
// ---------------------------------------------------------------------------

/** GET /api/v1/config response: full normalized config + its revision. */
export interface ConfigEnvelope {
  revision: string
  config: Config
}

/** PUT /api/v1/config request body. Omit revision to skip the conflict check. */
export interface ConfigUpdateRequest {
  revision?: string
  config: Config
}

/** POST /api/v1/config/validate response (200 even for invalid content). */
export interface ValidateResponse {
  valid: boolean
  issues: string[]
}

// ---------------------------------------------------------------------------
// Routes & transports (mutation envelopes)
// ---------------------------------------------------------------------------

export interface RoutesResponse {
  routes: RouteConfig[]
}

export interface TransportsResponse {
  transports: TransportConfig[]
}

/** POST /api/v1/routes and PUT /api/v1/routes/{name} request body. */
export interface RouteUpsertRequest {
  revision?: string
  route: RouteConfig
}

/** POST /api/v1/transports and PUT /api/v1/transports/{name} request body. */
export interface TransportUpsertRequest {
  revision?: string
  transport: TransportConfig
}

/** DELETE bodies carry an optional revision for optimistic concurrency. */
export interface RevisionRequest {
  revision?: string
}

// ---------------------------------------------------------------------------
// Metrics (internal/metrics Snapshot / RouteSnapshot)
// ---------------------------------------------------------------------------

export interface LatencySummary {
  count: number
  p50_ms: number
  p95_ms: number
  p99_ms: number
}

/** metrics.RouteSnapshot */
export interface RouteMetrics {
  name: string
  total: number
  status_2xx: number
  status_3xx: number
  status_4xx: number
  status_5xx: number
  upstream_errors: number
  active: number
  latency: LatencySummary
  last_request_at: TimeString | null
}

/** GET /api/v1/metrics: metrics.Snapshot merged with {"log_dropped": n}. */
export interface MetricsSnapshot {
  started_at: TimeString
  uptime_seconds: number
  total_requests: number
  error_requests: number
  active_requests: number
  active_websockets: number
  active_sse: number
  route_count: number
  transport_count: number
  latency: LatencySummary
  routes: RouteMetrics[]
  log_dropped: number
}

/** One hourly bucket of the 24h request history. */
export interface MetricsHistoryBucket {
  start: TimeString
  success: number
  errors: number
}

/** Sums over the whole 24h window. */
export interface MetricsHistoryTotals {
  success: number
  errors: number
}

/** GET /api/v1/metrics/history: 25 hourly buckets oldest→newest. */
export interface MetricsHistoryResponse {
  bucket_seconds: number
  buckets: MetricsHistoryBucket[]
  totals: MetricsHistoryTotals
}

// ---------------------------------------------------------------------------
// Logs (internal/logging AccessEntry)
// ---------------------------------------------------------------------------

/** One captured forward header (proxy metadata — never credentials). */
export interface ForwardHeader {
  name: string
  value: string
}

export interface AccessLogEntry {
  time: TimeString
  route: string
  method: string
  path: string
  status: number
  duration_ms: number
  transport: string
  streaming: StreamKind
  error: string
  /** Forward headers the client sent (Forwarded/Via/X-Forwarded-*); omitted when none. */
  forward_headers?: ForwardHeader[]
}

/** GET /api/v1/logs response. */
export interface LogsResponse {
  entries: AccessLogEntry[]
  dropped: number
  capacity: number
}

/** GET /api/v1/logs query parameters. */
export interface LogsQuery {
  limit?: number
  route?: string
  status_class?: StatusClass
}

// ---------------------------------------------------------------------------
// Diagnostics (internal/diagnostics)
// ---------------------------------------------------------------------------

/** POST /api/v1/diagnostics/request request body. Method defaults to GET. */
export interface RequestTestRequest {
  /** Full inbound path (must start with `/`; empty → `/`). */
  path?: string
  method?: string
}

/** POST /api/v1/diagnostics/route request body. Method defaults to GET. */
export interface RouteTestRequest {
  route: string
  path: string
  method?: string
}

/** POST /api/v1/diagnostics/transport request body. Method defaults to GET. */
export interface TransportTestRequest {
  transport: string
  url: string
  method?: string
}

/** diagnostics.Result */
export interface DiagnosticsResult {
  ok: boolean
  /** Matched/named route; empty when N/A (transport tests or no match). */
  route: string
  target_url: string
  transport: string
  status: number
  error_stage: DiagnosticStage
  error: string
  header_duration_ms: number
  total_duration_ms: number
}

// ---------------------------------------------------------------------------
// Errors
// ---------------------------------------------------------------------------

/** Client-visible error-code vocabulary (api/openapi.yaml → ApiError.error). */
export type ApiErrorCode =
  | 'route_not_found'
  | 'upstream_connection_failed'
  | 'upstream_timeout'
  | 'internal_error'
  | 'validation_failed'
  | 'revision_conflict'
  | 'not_found'
  | 'builtin_transport'
  | 'transport_in_use'
  | 'origin_not_allowed'
  | 'invalid_request'
  | 'method_not_allowed'
  | 'admin_disabled'
  | 'websocket_upgrade_failed'
  | (string & {})

/** Error response body: {"error":"<code>","detail":"...","issues":["..."]}. */
export interface ApiErrorBody {
  error: ApiErrorCode
  detail?: string
  /** Present only for validation_failed. */
  issues?: string[]
}
