package metrics

import (
	"sync"
	"testing"
	"time"
)

// All history tests drive observe/snapshot with injected times — never the
// wall clock — so they can't flake across real hour boundaries.

var histBase = time.Date(2026, 7, 12, 10, 30, 0, 0, time.UTC)

func TestHistoryBucketPlacementAndTotals(t *testing.T) {
	h := newHistory(histBase)
	h.observe(histBase, false)
	h.observe(histBase.Add(time.Minute), false)
	h.observe(histBase.Add(2*time.Minute), true)

	snap := h.snapshot(histBase.Add(3 * time.Minute))
	if snap.BucketSeconds != 3600 {
		t.Errorf("BucketSeconds = %d, want 3600", snap.BucketSeconds)
	}
	if len(snap.Buckets) != historySlots {
		t.Fatalf("len(Buckets) = %d, want %d", len(snap.Buckets), historySlots)
	}
	last := snap.Buckets[historySlots-1]
	if last.Success != 2 || last.Errors != 1 {
		t.Errorf("current bucket = %+v, want success=2 errors=1", last)
	}
	if want := histBase.Truncate(time.Hour); !last.Start.Equal(want) {
		t.Errorf("current bucket start = %v, want %v", last.Start, want)
	}
	if snap.Totals.Success != 2 || snap.Totals.Errors != 1 {
		t.Errorf("totals = %+v, want success=2 errors=1", snap.Totals)
	}
	// The series is contiguous hourly, oldest→newest.
	for i := 1; i < len(snap.Buckets); i++ {
		if snap.Buckets[i].Start.Sub(snap.Buckets[i-1].Start) != time.Hour {
			t.Fatalf("buckets not contiguous at %d: %v then %v",
				i, snap.Buckets[i-1].Start, snap.Buckets[i].Start)
		}
	}
	// Hours before construction read as zero.
	if first := snap.Buckets[0]; first.Success != 0 || first.Errors != 0 {
		t.Errorf("pre-start bucket = %+v, want zeros", first)
	}
}

func TestHistoryRotation(t *testing.T) {
	h := newHistory(histBase)
	h.observe(histBase, false)
	h.observe(histBase.Add(time.Hour), true)

	snap := h.snapshot(histBase.Add(time.Hour))
	if b := snap.Buckets[historySlots-1]; b.Errors != 1 || b.Success != 0 {
		t.Errorf("newest bucket = %+v, want errors=1", b)
	}
	if b := snap.Buckets[historySlots-2]; b.Success != 1 || b.Errors != 0 {
		t.Errorf("previous bucket = %+v, want success=1", b)
	}

	// A 3-hour gap zeroes the skipped hours (histBase+2h and +3h).
	h.observe(histBase.Add(4*time.Hour), false)
	snap = h.snapshot(histBase.Add(4 * time.Hour))
	for _, idx := range []int{historySlots - 2, historySlots - 3} {
		if b := snap.Buckets[idx]; b.Success != 0 || b.Errors != 0 {
			t.Errorf("skipped bucket %d = %+v, want zeros", idx, b)
		}
	}
	if snap.Totals.Success != 2 || snap.Totals.Errors != 1 {
		t.Errorf("totals after gap = %+v, want success=2 errors=1", snap.Totals)
	}
}

func TestHistoryFullResetAfterLongIdle(t *testing.T) {
	h := newHistory(histBase)
	h.observe(histBase, false)

	// Idle longer than the whole window: everything before is stale.
	late := histBase.Add((historySlots + 11) * time.Hour)
	h.observe(late, true)
	snap := h.snapshot(late)
	if snap.Totals.Success != 0 || snap.Totals.Errors != 1 {
		t.Errorf("totals after reset = %+v, want success=0 errors=1", snap.Totals)
	}
	if b := snap.Buckets[historySlots-1]; b.Errors != 1 {
		t.Errorf("newest bucket after reset = %+v, want errors=1", b)
	}
}

func TestHistoryClockBackwards(t *testing.T) {
	h := newHistory(histBase)
	h.observe(histBase, false)
	// NTP step: an observation from the previous hour clamps into the
	// newest slot instead of rotating backwards.
	h.observe(histBase.Add(-time.Hour), true)

	snap := h.snapshot(histBase)
	if b := snap.Buckets[historySlots-1]; b.Success != 1 || b.Errors != 1 {
		t.Errorf("newest bucket = %+v, want success=1 errors=1 (clamped)", b)
	}
	if snap.Totals.Success != 1 || snap.Totals.Errors != 1 {
		t.Errorf("totals = %+v, want success=1 errors=1", snap.Totals)
	}
}

func TestHistorySnapshotRotatesWhenIdle(t *testing.T) {
	h := newHistory(histBase)
	h.observe(histBase, false)

	// No observations for two hours: snapshot itself must advance the ring
	// so the "current" bucket is the current hour, not a stale partial.
	at := histBase.Add(2 * time.Hour)
	snap := h.snapshot(at)
	last := snap.Buckets[historySlots-1]
	if want := at.Truncate(time.Hour); !last.Start.Equal(want) {
		t.Errorf("current bucket start = %v, want %v", last.Start, want)
	}
	if last.Success != 0 || last.Errors != 0 {
		t.Errorf("current bucket = %+v, want zeros after idle", last)
	}
	if b := snap.Buckets[historySlots-3]; b.Success != 1 {
		t.Errorf("bucket two hours back = %+v, want success=1", b)
	}
}

func TestRegistryHistoryConcurrent(t *testing.T) {
	r := NewRegistry()
	r.SetRoutes([]string{"a"})

	const goroutines = 8
	const iterations = 500
	var wg sync.WaitGroup
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < iterations; i++ {
				r.Observe("a", 200, false, time.Millisecond)
				if i%50 == 0 {
					_ = r.History()
				}
			}
		}()
	}
	wg.Wait()

	snap := r.History()
	if got := snap.Totals.Success; got != goroutines*iterations {
		t.Errorf("history totals success = %d, want %d", got, goroutines*iterations)
	}
	if snap.Totals.Errors != 0 {
		t.Errorf("history totals errors = %d, want 0", snap.Totals.Errors)
	}
}
