package metrics

import (
	"sync"
	"sync/atomic"
	"time"
)

// historySlots is the ring size for the 48h request history: 48 sealed
// hourly buckets plus the current partial hour. State is fixed-size
// (§22.2): 49 slots of three integers, no per-request allocation.
const historySlots = 49

// historyBucketSeconds is the width of one history bucket.
const historyBucketSeconds = 3600

// historyShards spreads observe() locks across independent rings so multi-
// core request completions do not serialize on one mutex. Snapshot sums
// matching hour slots across shards. Semantics (totals per hour) are
// unchanged; only lock contention is reduced.
const historyShards = 16

// historySlot is one hourly bucket. hour is the unix epoch-hour the slot
// covers; a slot is only trusted when its hour matches the position the
// ring expects, so stale slots read as zero without being scrubbed.
type historySlot struct {
	hour    int64
	success uint64
	errors  uint64
}

// historyShard is one independent fixed-size hour ring.
type historyShard struct {
	mu    sync.Mutex
	slots [historySlots]historySlot
	cur   int // index of the newest (current partial hour) slot
}

// history is the rolling 48h success/error series (dashboard charts).
type history struct {
	shards [historyShards]historyShard
	// rr picks the next shard without contention on a shared counter path
	// that would itself become a hotspot (Add is enough).
	rr atomic.Uint64
}

func newHistory(now time.Time) *history {
	h := &history{}
	hour := epochHour(now)
	for i := range h.shards {
		h.shards[i].slots[0] = historySlot{hour: hour}
	}
	return h
}

func epochHour(t time.Time) int64 { return t.Unix() / historyBucketSeconds }

// rotateLocked advances the ring so the newest slot covers nowHour. Both
// observe and snapshot call it, so an idle stretch can never leave a stale
// "current" bucket. Rotation is lazy — no background timer.
func (s *historyShard) rotateLocked(nowHour int64) {
	gap := nowHour - s.slots[s.cur].hour
	if gap <= 0 {
		// Same hour, or the clock stepped backwards (NTP): clamp into the
		// newest slot rather than rotating backwards.
		return
	}
	if gap >= historySlots {
		// Idle for longer than the whole window: every slot is stale.
		s.slots = [historySlots]historySlot{}
		s.slots[0] = historySlot{hour: nowHour}
		s.cur = 0
		return
	}
	prev := s.slots[s.cur].hour
	for i := int64(1); i <= gap; i++ {
		s.cur = (s.cur + 1) % historySlots
		s.slots[s.cur] = historySlot{hour: prev + i}
	}
}

// observe records one completed request into the current hourly bucket of
// a round-robin shard.
func (h *history) observe(now time.Time, isErr bool) {
	hour := epochHour(now)
	idx := h.rr.Add(1) % historyShards
	s := &h.shards[idx]
	s.mu.Lock()
	s.rotateLocked(hour)
	if isErr {
		s.slots[s.cur].errors++
	} else {
		s.slots[s.cur].success++
	}
	s.mu.Unlock()
}

// snapshot returns exactly historySlots buckets oldest→newest, ending with
// the current partial hour. Hours the ring never saw (pre-startup, after a
// full reset) come back as zeros with a synthesized start time, so the
// series is always fixed-length and contiguous.
func (h *history) snapshot(now time.Time) HistorySnapshot {
	nowHour := epochHour(now)
	out := HistorySnapshot{
		BucketSeconds: historyBucketSeconds,
		Buckets:       make([]HistoryBucket, historySlots),
	}

	// Rotate every shard to nowHour and sum per expected hour.
	// Hold each shard lock only for its rotate + read of 49 slots.
	var totals [historySlots]struct{ ok, err uint64 }

	for i := range h.shards {
		s := &h.shards[i]
		s.mu.Lock()
		s.rotateLocked(nowHour)
		for k := 0; k < historySlots; k++ {
			expected := nowHour - int64(historySlots-1-k)
			if slot := s.slots[(s.cur+1+k)%historySlots]; slot.hour == expected {
				totals[k].ok += slot.success
				totals[k].err += slot.errors
			}
		}
		s.mu.Unlock()
	}

	for k := 0; k < historySlots; k++ {
		expected := nowHour - int64(historySlots-1-k)
		b := HistoryBucket{
			Start:   time.Unix(expected*historyBucketSeconds, 0).UTC(),
			Success: totals[k].ok,
			Errors:  totals[k].err,
		}
		out.Buckets[k] = b
		out.Totals.Success += b.Success
		out.Totals.Errors += b.Errors
	}
	return out
}
