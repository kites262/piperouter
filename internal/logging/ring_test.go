package logging

import (
	"strconv"
	"sync"
	"testing"
)

func entry(route string, status int, errCode string) AccessEntry {
	return AccessEntry{Route: route, Method: "GET", Path: "/p", Status: status, Error: errCode}
}

func paths(entries []AccessEntry) []string {
	out := make([]string, len(entries))
	for i, e := range entries {
		out[i] = e.Path
	}
	return out
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// addSeq adds n entries with Path "/1".."/n" (so "/n" is newest).
func addSeq(r *Ring, n int) {
	for i := 1; i <= n; i++ {
		r.Add(AccessEntry{Route: "r", Method: "GET", Path: "/" + strconv.Itoa(i), Status: 200})
	}
}

func TestRingOverflowKeepsNewestFirst(t *testing.T) {
	r := NewRing(3)
	addSeq(r, 5)

	got := r.Snapshot(0, "", "")
	if want := []string{"/5", "/4", "/3"}; !equalStrings(paths(got), want) {
		t.Errorf("Snapshot = %v, want %v (newest first)", paths(got), want)
	}
	if d := r.Dropped(); d != 2 {
		t.Errorf("Dropped = %d, want 2", d)
	}
	if c := r.Capacity(); c != 3 {
		t.Errorf("Capacity = %d, want 3", c)
	}
}

func TestRingPartialFill(t *testing.T) {
	r := NewRing(10)
	addSeq(r, 3)
	got := r.Snapshot(0, "", "")
	if want := []string{"/3", "/2", "/1"}; !equalStrings(paths(got), want) {
		t.Errorf("Snapshot = %v, want %v", paths(got), want)
	}
	if d := r.Dropped(); d != 0 {
		t.Errorf("Dropped = %d, want 0", d)
	}
}

func TestRingSnapshotFilters(t *testing.T) {
	fill := func() *Ring {
		r := NewRing(10)
		r.Add(entry("a", 200, ""))
		r.Add(entry("b", 301, ""))
		r.Add(entry("a", 404, ""))
		r.Add(entry("b", 503, "upstream_timeout"))
		r.Add(entry("a", 200, "client_canceled"))
		return r
	}

	tests := []struct {
		name        string
		limit       int
		route       string
		statusClass string
		wantLen     int
		check       func(t *testing.T, got []AccessEntry)
	}{
		{
			name: "no filters returns all newest first", wantLen: 5,
			check: func(t *testing.T, got []AccessEntry) {
				if got[0].Status != 200 || got[0].Error != "client_canceled" {
					t.Errorf("first entry = %+v, want the newest one", got[0])
				}
				if got[4].Status != 200 || got[4].Route != "a" || got[4].Error != "" {
					t.Errorf("last entry = %+v, want the oldest one", got[4])
				}
			},
		},
		{
			name: "route filter exact", route: "a", wantLen: 3,
			check: func(t *testing.T, got []AccessEntry) {
				for _, e := range got {
					if e.Route != "a" {
						t.Errorf("entry route = %q, want a", e.Route)
					}
				}
			},
		},
		{
			name: "2xx class", statusClass: "2xx", wantLen: 2,
			check: func(t *testing.T, got []AccessEntry) {
				for _, e := range got {
					if e.Status/100 != 2 {
						t.Errorf("entry status = %d, want 2xx", e.Status)
					}
				}
			},
		},
		{name: "3xx class", statusClass: "3xx", wantLen: 1},
		{name: "4xx class", statusClass: "4xx", wantLen: 1},
		{name: "5xx class", statusClass: "5xx", wantLen: 1},
		{
			name: "error class means Error non-empty", statusClass: "error", wantLen: 2,
			check: func(t *testing.T, got []AccessEntry) {
				for _, e := range got {
					if e.Error == "" {
						t.Errorf("entry %+v has empty Error", e)
					}
				}
			},
		},
		{name: "route and class combined", route: "a", statusClass: "2xx", wantLen: 2},
		{name: "unknown class matches nothing", statusClass: "9xx", wantLen: 0},
		{name: "limit caps results", limit: 2, wantLen: 2},
		{name: "limit larger than matches", limit: 100, statusClass: "3xx", wantLen: 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := fill()
			got := r.Snapshot(tt.limit, tt.route, tt.statusClass)
			if len(got) != tt.wantLen {
				t.Fatalf("len = %d, want %d (%+v)", len(got), tt.wantLen, got)
			}
			if tt.check != nil {
				tt.check(t, got)
			}
		})
	}
}

func TestRingLimitReturnsNewest(t *testing.T) {
	r := NewRing(10)
	addSeq(r, 5)
	got := r.Snapshot(2, "", "")
	if want := []string{"/5", "/4"}; !equalStrings(paths(got), want) {
		t.Errorf("Snapshot(limit=2) = %v, want %v", paths(got), want)
	}
}

func TestSetCapacityShrinkKeepsNewest(t *testing.T) {
	r := NewRing(5)
	addSeq(r, 5)

	r.SetCapacity(2)
	if c := r.Capacity(); c != 2 {
		t.Fatalf("Capacity = %d, want 2", c)
	}
	got := r.Snapshot(0, "", "")
	if want := []string{"/5", "/4"}; !equalStrings(paths(got), want) {
		t.Errorf("after shrink Snapshot = %v, want %v", paths(got), want)
	}

	// The resized ring must keep rotating correctly.
	r.Add(AccessEntry{Path: "/6", Status: 200})
	got = r.Snapshot(0, "", "")
	if want := []string{"/6", "/5"}; !equalStrings(paths(got), want) {
		t.Errorf("after add post-shrink Snapshot = %v, want %v", paths(got), want)
	}
	if d := r.Dropped(); d != 1 {
		t.Errorf("Dropped = %d, want 1 (resize itself does not count)", d)
	}
}

func TestSetCapacityGrowKeepsAll(t *testing.T) {
	r := NewRing(2)
	addSeq(r, 2)
	r.SetCapacity(4)
	if c := r.Capacity(); c != 4 {
		t.Fatalf("Capacity = %d, want 4", c)
	}
	r.Add(AccessEntry{Path: "/3", Status: 200})
	r.Add(AccessEntry{Path: "/4", Status: 200})
	got := r.Snapshot(0, "", "")
	if want := []string{"/4", "/3", "/2", "/1"}; !equalStrings(paths(got), want) {
		t.Errorf("after grow Snapshot = %v, want %v", paths(got), want)
	}
	if d := r.Dropped(); d != 0 {
		t.Errorf("Dropped = %d, want 0", d)
	}
}

func TestRingDisabled(t *testing.T) {
	for _, capacity := range []int{0, -1} {
		r := NewRing(capacity)
		r.Add(entry("a", 200, ""))
		r.Add(entry("a", 500, "x"))
		if got := r.Snapshot(0, "", ""); len(got) != 0 {
			t.Errorf("NewRing(%d): Snapshot len = %d, want 0", capacity, len(got))
		}
		if d := r.Dropped(); d != 0 {
			t.Errorf("NewRing(%d): Dropped = %d, want 0 (disabled drops silently)", capacity, d)
		}
		if c := r.Capacity(); c != 0 {
			t.Errorf("NewRing(%d): Capacity = %d, want 0", capacity, c)
		}
	}
}

func TestSetCapacityEnableAndDisable(t *testing.T) {
	r := NewRing(0)
	r.Add(entry("a", 200, "")) // dropped silently while disabled

	r.SetCapacity(2) // hot-enable
	addSeq(r, 2)
	if got := r.Snapshot(0, "", ""); len(got) != 2 {
		t.Fatalf("after enable Snapshot len = %d, want 2", len(got))
	}

	r.SetCapacity(0) // hot-disable clears everything
	if got := r.Snapshot(0, "", ""); len(got) != 0 {
		t.Errorf("after disable Snapshot len = %d, want 0", len(got))
	}
	if c := r.Capacity(); c != 0 {
		t.Errorf("after disable Capacity = %d, want 0", c)
	}
	r.Add(entry("a", 200, ""))
	if got := r.Snapshot(0, "", ""); len(got) != 0 {
		t.Errorf("Add after disable stored an entry: %v", got)
	}
}

func TestRingConcurrentAdd(t *testing.T) {
	const workers = 8
	const perWorker = 100
	const capacity = 16

	r := NewRing(capacity)
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < perWorker; i++ {
				r.Add(entry("r"+strconv.Itoa(w), 200, ""))
				if i%10 == 0 {
					_ = r.Snapshot(5, "", "")
					_ = r.Dropped()
				}
			}
		}(w)
	}
	wg.Wait()

	if got := len(r.Snapshot(0, "", "")); got != capacity {
		t.Errorf("Snapshot len = %d, want %d", got, capacity)
	}
	const total = workers * perWorker
	if d := r.Dropped(); d != total-capacity {
		t.Errorf("Dropped = %d, want %d", d, total-capacity)
	}
}

func TestRingEnabled(t *testing.T) {
	if NewRing(0).Enabled() {
		t.Error("NewRing(0).Enabled() = true, want false")
	}
	r := NewRing(4)
	if !r.Enabled() {
		t.Error("NewRing(4).Enabled() = false, want true")
	}
	r.SetCapacity(0)
	if r.Enabled() {
		t.Error("Enabled() = true after SetCapacity(0)")
	}
	r.SetCapacity(8)
	if !r.Enabled() {
		t.Error("Enabled() = false after SetCapacity(8)")
	}
}
