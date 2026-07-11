package metrics

import (
	"sync"
	"time"
)

// historySlots is the ring size for the 48h request history: 48 sealed
// hourly buckets plus the current partial hour. State is fixed-size
// (§22.2): 49 slots of three integers, no per-request allocation.
const historySlots = 49

// historyBucketSeconds is the width of one history bucket.
const historyBucketSeconds = 3600

// historySlot is one hourly bucket. hour is the unix epoch-hour the slot
// covers; a slot is only trusted when its hour matches the position the
// ring expects, so stale slots read as zero without being scrubbed.
type historySlot struct {
	hour    int64
	success uint64
	errors  uint64
}

// history is the rolling 48h success/error series (dashboard charts). A
// single mutex guards rotation and counting: the critical section is two
// integer adds, far below the per-request budget (the access-log ring
// already serializes every request through a larger one).
type history struct {
	mu    sync.Mutex
	slots [historySlots]historySlot
	cur   int // index of the newest (current partial hour) slot
}

func newHistory(now time.Time) *history {
	h := &history{}
	h.slots[0] = historySlot{hour: epochHour(now)}
	return h
}

func epochHour(t time.Time) int64 { return t.Unix() / historyBucketSeconds }

// rotateLocked advances the ring so the newest slot covers nowHour. Both
// observe and snapshot call it, so an idle stretch can never leave a stale
// "current" bucket. Rotation is lazy — no background timer.
func (h *history) rotateLocked(nowHour int64) {
	gap := nowHour - h.slots[h.cur].hour
	if gap <= 0 {
		// Same hour, or the clock stepped backwards (NTP): clamp into the
		// newest slot rather than rotating backwards.
		return
	}
	if gap >= historySlots {
		// Idle for longer than the whole window: every slot is stale.
		h.slots = [historySlots]historySlot{}
		h.slots[0] = historySlot{hour: nowHour}
		h.cur = 0
		return
	}
	prev := h.slots[h.cur].hour
	for i := int64(1); i <= gap; i++ {
		h.cur = (h.cur + 1) % historySlots
		h.slots[h.cur] = historySlot{hour: prev + i}
	}
}

// observe records one completed request into the current hourly bucket.
func (h *history) observe(now time.Time, isErr bool) {
	hour := epochHour(now)
	h.mu.Lock()
	h.rotateLocked(hour)
	if isErr {
		h.slots[h.cur].errors++
	} else {
		h.slots[h.cur].success++
	}
	h.mu.Unlock()
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

	h.mu.Lock()
	h.rotateLocked(nowHour)
	for k := 0; k < historySlots; k++ {
		expected := nowHour - int64(historySlots-1-k)
		b := HistoryBucket{Start: time.Unix(expected*historyBucketSeconds, 0).UTC()}
		if s := h.slots[(h.cur+1+k)%historySlots]; s.hour == expected {
			b.Success = s.success
			b.Errors = s.errors
		}
		out.Buckets[k] = b
		out.Totals.Success += b.Success
		out.Totals.Errors += b.Errors
	}
	h.mu.Unlock()
	return out
}
