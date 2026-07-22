package logging

import (
	"sync"
	"sync/atomic"
)

// Ring is a fixed-capacity in-memory circular buffer of recent access
// entries (PRD §14.2). Add is O(1) under a mutex and never performs I/O, so
// it cannot block the data plane (§14.4). When full, the oldest entry is
// overwritten and the dropped counter increments. A capacity <= 0 disables
// the ring entirely: Add becomes a no-op (not counted as dropped),
// snapshots are empty.
type Ring struct {
	mu      sync.Mutex
	buf     []AccessEntry // nil/empty when disabled
	next    int           // index of the next write
	size    int           // number of valid entries, <= len(buf)
	dropped uint64        // overwritten-by-overflow count
	enabled atomic.Bool   // len(buf) > 0, readable without the mutex
}

// NewRing creates a ring buffer. capacity <= 0 returns a disabled ring.
func NewRing(capacity int) *Ring {
	r := &Ring{}
	if capacity > 0 {
		r.buf = make([]AccessEntry, capacity)
		r.enabled.Store(true)
	}
	return r
}

// Enabled reports whether the ring currently stores entries. Lock-free, so
// the data plane can skip per-entry work for a disabled ring cheaply.
func (r *Ring) Enabled() bool { return r.enabled.Load() }

// Add appends an entry, overwriting the oldest when full. No-op when
// disabled.
func (r *Ring) Add(e AccessEntry) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.buf) == 0 {
		return
	}
	if r.size == len(r.buf) {
		r.dropped++
	} else {
		r.size++
	}
	r.buf[r.next] = e
	r.next = (r.next + 1) % len(r.buf)
}

// Snapshot returns matching entries newest-first. limit <= 0 means all.
// route filters by exact route name when non-empty. statusClass is "" (all),
// "2xx".."5xx" (by status/100) or "error" (Error != ""); any other value
// matches nothing.
//
// The mutex is held only long enough to copy the live window in newest-first
// order; route/status filtering and limit run without the lock so concurrent
// Add on the data plane is not stalled by admin UI polls.
func (r *Ring) Snapshot(limit int, route, statusClass string) []AccessEntry {
	r.mu.Lock()
	if r.size == 0 {
		r.mu.Unlock()
		return []AccessEntry{}
	}
	n := r.size
	bufLen := len(r.buf)
	// Point-in-time copy under lock (same consistency as filtering under lock).
	tmp := make([]AccessEntry, n)
	for i := 0; i < n; i++ {
		tmp[i] = r.buf[(r.next-1-i+2*bufLen)%bufLen]
	}
	r.mu.Unlock()

	if route == "" && statusClass == "" {
		if limit > 0 && limit < len(tmp) {
			return tmp[:limit]
		}
		return tmp
	}

	capHint := n
	if limit > 0 && limit < n {
		capHint = limit
	}
	out := make([]AccessEntry, 0, capHint)
	for _, e := range tmp {
		if route != "" && e.Route != route {
			continue
		}
		if !matchStatusClass(e, statusClass) {
			continue
		}
		out = append(out, e)
		if limit > 0 && len(out) >= limit {
			break
		}
	}
	return out
}

func matchStatusClass(e AccessEntry, class string) bool {
	switch class {
	case "":
		return true
	case "error":
		return e.Error != ""
	case "2xx", "3xx", "4xx", "5xx":
		return e.Status/100 == int(class[0]-'0')
	default:
		return false
	}
}

// SetCapacity hot-resizes the buffer, preserving the newest min(n, size)
// entries in order. n <= 0 disables the ring and clears it. The dropped
// counter is not affected by resizing.
func (r *Ring) SetCapacity(n int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.enabled.Store(n > 0)
	if n <= 0 {
		r.buf, r.next, r.size = nil, 0, 0
		return
	}
	if n == len(r.buf) {
		return
	}
	keep := min(r.size, n)
	newBuf := make([]AccessEntry, n)
	old := len(r.buf)
	for i := 0; i < keep; i++ { // chronological order of the newest `keep`
		newBuf[i] = r.buf[(r.next-keep+i+2*old)%old]
	}
	r.buf = newBuf
	r.size = keep
	r.next = keep % n
}

// Capacity returns the current buffer capacity (0 when disabled).
func (r *Ring) Capacity() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.buf)
}

// Dropped returns how many entries were overwritten due to overflow.
func (r *Ring) Dropped() uint64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.dropped
}
