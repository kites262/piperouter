/** Shared chart math + label helpers for the dashboard charts (no rendering). */

/** Smallest "nice" number (1, 2 or 5 × 10^k) >= n; non-positive → 1. */
export function niceCeil(n: number): number {
  if (!Number.isFinite(n) || n <= 0) return 1
  const base = Math.pow(10, Math.floor(Math.log10(n)))
  for (const m of [1, 2, 5]) {
    if (m * base >= n) return m * base
  }
  return 10 * base
}

/** Gridline values for a niceCeil max: the midpoint too when it is clean. */
export function yTicks(max: number): number[] {
  const half = max / 2
  return Number.isInteger(half) ? [half, max] : [max]
}

/** Local "14:00" for an ISO bucket start (24h clock, repo convention). */
export function hourLabel(iso: string): string {
  return new Date(iso).toLocaleTimeString(undefined, {
    hour: '2-digit',
    minute: '2-digit',
    hour12: false,
  })
}

/** Local short date, e.g. "7/12", for an ISO bucket start. */
export function dayLabel(iso: string): string {
  return new Date(iso).toLocaleDateString(undefined, { month: 'numeric', day: 'numeric' })
}

/** True when the ISO bucket start is a local midnight (00:00). */
export function isLocalMidnight(iso: string): boolean {
  const d = new Date(iso)
  return d.getHours() === 0 && d.getMinutes() === 0
}

/** Local "14:00 – 15:00" for an ISO bucket start. */
export function hourRangeLabel(iso: string): string {
  const start = new Date(iso)
  const end = new Date(start.getTime() + 3_600_000)
  return `${hourLabel(start.toISOString())} – ${hourLabel(end.toISOString())}`
}

/**
 * Path for a bar segment rounded only at the top, square at the base —
 * data-end caps point away from the baseline. r clamps to the segment size.
 */
export function topRoundedRect(x: number, y: number, w: number, h: number, r: number): string {
  const rr = Math.min(r, h, w / 2)
  const right = x + w
  return (
    `M${x},${y + h} L${x},${y + rr} Q${x},${y} ${x + rr},${y}` +
    ` L${right - rr},${y} Q${right},${y} ${right},${y + rr} L${right},${y + h} Z`
  )
}
