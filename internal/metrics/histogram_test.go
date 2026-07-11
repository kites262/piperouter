package metrics

import (
	"math"
	"testing"
)

const eps = 1e-9

func almostEqual(a, b float64) bool { return math.Abs(a-b) < eps }

func TestHistogramSummary(t *testing.T) {
	repeat := func(v float64, n int) []float64 {
		out := make([]float64, n)
		for i := range out {
			out[i] = v
		}
		return out
	}

	tests := []struct {
		name         string
		observations []float64 // milliseconds
		wantCount    uint64
		wantP50      float64
		wantP95      float64
		wantP99      float64
	}{
		{
			name:         "empty histogram reports zero percentiles",
			observations: nil,
			wantCount:    0, wantP50: 0, wantP95: 0, wantP99: 0,
		},
		{
			// All samples land in the (2,5] bucket; interpolation keeps
			// every percentile inside that bucket.
			name:         "100 observations of 3ms",
			observations: repeat(3, 100),
			wantCount:    100,
			wantP50:      2 + 0.50*3, // 3.5
			wantP95:      2 + 0.95*3, // 4.85
			wantP99:      2 + 0.99*3, // 4.97
		},
		{
			name:         "first bucket lower bound is zero",
			observations: repeat(0.5, 10),
			wantCount:    10,
			wantP50:      0.5,
			wantP95:      0.95,
			wantP99:      0.99,
		},
		{
			name:         "split across two buckets",
			observations: append(repeat(0.5, 50), repeat(80, 50)...),
			wantCount:    100,
			wantP50:      1,  // rank 50 exhausts the [0,1] bucket exactly
			wantP95:      95, // 50 + 0.9*(100-50)
			wantP99:      99, // 50 + 0.98*(100-50)
		},
		{
			name:         "overflow bucket reports last finite bound",
			observations: repeat(100000, 10),
			wantCount:    10,
			wantP50:      60000, wantP95: 60000, wantP99: 60000,
		},
		{
			name:         "tail in overflow bucket",
			observations: append(repeat(3, 90), repeat(70000, 10)...),
			wantCount:    100,
			wantP50:      2 + 3*50.0/90.0,
			wantP95:      60000,
			wantP99:      60000,
		},
		{
			name:         "boundary value goes to lower bucket",
			observations: repeat(2, 4), // exactly the (1,2] upper bound
			wantCount:    4,
			wantP50:      1.5,
			wantP95:      1.95,
			wantP99:      1.99,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var h histogram
			for _, ms := range tt.observations {
				h.observe(ms)
			}
			got := h.summary()
			if got.Count != tt.wantCount {
				t.Errorf("Count = %d, want %d", got.Count, tt.wantCount)
			}
			if !almostEqual(got.P50Ms, tt.wantP50) {
				t.Errorf("P50Ms = %v, want %v", got.P50Ms, tt.wantP50)
			}
			if !almostEqual(got.P95Ms, tt.wantP95) {
				t.Errorf("P95Ms = %v, want %v", got.P95Ms, tt.wantP95)
			}
			if !almostEqual(got.P99Ms, tt.wantP99) {
				t.Errorf("P99Ms = %v, want %v", got.P99Ms, tt.wantP99)
			}
		})
	}
}

func TestHistogramPercentilesWithinWinningBucket(t *testing.T) {
	var h histogram
	for i := 0; i < 100; i++ {
		h.observe(3)
	}
	s := h.summary()
	for name, p := range map[string]float64{"P50": s.P50Ms, "P95": s.P95Ms, "P99": s.P99Ms} {
		if p <= 2 || p > 5 {
			t.Errorf("%s = %v, want within (2,5]", name, p)
		}
	}
}
