package metrics

import (
	"testing"
	"time"
)

func BenchmarkHistoryObserve(b *testing.B) {
	h := newHistory(time.Now())
	now := time.Now()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		h.observe(now, i%10 == 0)
	}
}

func BenchmarkHistorySnapshot(b *testing.B) {
	h := newHistory(time.Now())
	now := time.Now()
	for i := 0; i < 1000; i++ {
		h.observe(now, i%10 == 0)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = h.snapshot(now)
	}
}
