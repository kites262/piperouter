package metrics

import (
	"sort"
	"sync/atomic"
)

// bucketBounds are the fixed latency histogram bucket upper bounds in
// milliseconds (PRD §13.3). An implicit +Inf overflow bucket follows the
// last finite bound. The set is fixed at compile time so per-route memory
// stays bounded (§22.2).
var bucketBounds = [...]float64{
	1, 2, 5, 10, 25, 50, 100, 250, 500, 1000, 2500, 5000, 10000, 30000, 60000,
}

// numBuckets includes the +Inf overflow bucket.
const numBuckets = len(bucketBounds) + 1

// histogram is a fixed-bucket, lock-free latency histogram. All updates are
// single atomic increments so observing never blocks the data plane (§13.4).
type histogram struct {
	buckets [numBuckets]atomic.Uint64
}

// observe records one latency sample given in milliseconds.
func (h *histogram) observe(ms float64) {
	// Smallest index whose upper bound is >= ms; falls through to the
	// overflow bucket when ms exceeds the last finite bound.
	idx := sort.SearchFloat64s(bucketBounds[:], ms)
	h.buckets[idx].Add(1)
}

// summary loads the buckets once and derives Count/P50/P95/P99. With zero
// observations all percentiles are 0.
func (h *histogram) summary() LatencySummary {
	var counts [numBuckets]uint64
	var total uint64
	for i := range h.buckets {
		c := h.buckets[i].Load()
		counts[i] = c
		total += c
	}
	if total == 0 {
		return LatencySummary{}
	}
	return LatencySummary{
		Count: total,
		P50Ms: percentile(&counts, total, 0.50),
		P95Ms: percentile(&counts, total, 0.95),
		P99Ms: percentile(&counts, total, 0.99),
	}
}

// percentile computes quantile q by linear interpolation inside the winning
// bucket. The lower bound of the first bucket is 0; the +Inf overflow bucket
// reports the last finite bound.
func percentile(counts *[numBuckets]uint64, total uint64, q float64) float64 {
	rank := q * float64(total)
	var cum float64
	for i, c := range counts {
		if c == 0 {
			continue
		}
		prev := cum
		cum += float64(c)
		if cum < rank {
			continue
		}
		if i == len(bucketBounds) { // +Inf overflow bucket
			return bucketBounds[len(bucketBounds)-1]
		}
		lower := 0.0
		if i > 0 {
			lower = bucketBounds[i-1]
		}
		upper := bucketBounds[i]
		fraction := (rank - prev) / float64(c)
		if fraction < 0 {
			fraction = 0
		}
		return lower + fraction*(upper-lower)
	}
	// Unreachable when total > 0; keep a sane fallback.
	return bucketBounds[len(bucketBounds)-1]
}
