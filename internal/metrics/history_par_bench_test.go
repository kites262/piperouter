package metrics

import (
	"sync"
	"testing"
	"time"
)

// Parallel observe to stress lock contention (production multi-core path).
func BenchmarkHistoryObserveParallel(b *testing.B) {
	h := newHistory(time.Now())
	now := time.Now()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			h.observe(now, i%10 == 0)
			i++
		}
	})
}

func BenchmarkHistoryObserveContended(b *testing.B) {
	h := newHistory(time.Now())
	now := time.Now()
	const workers = 32
	b.ResetTimer()
	var wg sync.WaitGroup
	// Each b.N is split across workers roughly equally.
	n := b.N
	per := (n + workers - 1) / workers
	wg.Add(workers)
	for w := 0; w < workers; w++ {
		go func() {
			defer wg.Done()
			for i := 0; i < per; i++ {
				h.observe(now, i%10 == 0)
			}
		}()
	}
	wg.Wait()
}
