package metrics

import "time"

// LatencySummary condenses a latency histogram into count + percentiles.
type LatencySummary struct {
	Count uint64  `json:"count"`
	P50Ms float64 `json:"p50_ms"`
	P95Ms float64 `json:"p95_ms"`
	P99Ms float64 `json:"p99_ms"`
}

// RouteSnapshot is a point-in-time view of one route's counters (PRD §13.3).
type RouteSnapshot struct {
	Name           string         `json:"name"`
	Total          uint64         `json:"total"`
	Status2xx      uint64         `json:"status_2xx"`
	Status3xx      uint64         `json:"status_3xx"`
	Status4xx      uint64         `json:"status_4xx"`
	Status5xx      uint64         `json:"status_5xx"`
	UpstreamErrors uint64         `json:"upstream_errors"`
	Active         uint64         `json:"active"`
	Latency        LatencySummary `json:"latency"`
	LastRequestAt  *time.Time     `json:"last_request_at"` // nil if never
}

// HistoryBucket is one hourly bucket of the 48h request history. Start is
// the UTC beginning of the hour the bucket covers.
type HistoryBucket struct {
	Start   time.Time `json:"start"`
	Success uint64    `json:"success"`
	Errors  uint64    `json:"errors"` // 5xx + upstream errors, same rule as Snapshot.ErrorRequests
}

// HistoryTotals sums the buckets of one history window.
type HistoryTotals struct {
	Success uint64 `json:"success"`
	Errors  uint64 `json:"errors"`
}

// HistorySnapshot is the fixed-length 48h series: buckets oldest→newest,
// the last one being the current partial hour.
type HistorySnapshot struct {
	BucketSeconds int             `json:"bucket_seconds"`
	Buckets       []HistoryBucket `json:"buckets"`
	Totals        HistoryTotals   `json:"totals"`
}

// Snapshot is a point-in-time view of all metrics (PRD §13.2).
type Snapshot struct {
	StartedAt        time.Time       `json:"started_at"`
	UptimeSeconds    float64         `json:"uptime_seconds"`
	TotalRequests    uint64          `json:"total_requests"`
	ErrorRequests    uint64          `json:"error_requests"` // 5xx + upstream errors
	ActiveRequests   uint64          `json:"active_requests"`
	ActiveWebSockets uint64          `json:"active_websockets"`
	ActiveSSE        uint64          `json:"active_sse"`
	RouteCount       int             `json:"route_count"`
	TransportCount   int             `json:"transport_count"`
	Latency          LatencySummary  `json:"latency"`
	Routes           []RouteSnapshot `json:"routes"` // sorted by name
}
