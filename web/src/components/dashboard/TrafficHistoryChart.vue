<!-- 24h traffic history — hourly success/error stacked bars (liquid glass).
     Drawn in measured pixels (ResizeObserver → viewBox) so hairlines, the
     2px stack gap and the rounded caps never distort. Series are states, so
     they wear the theme status palette; identity never rides color alone
     (legend + fixed stack order + tooltip). -->
<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'

import type { MetricsHistoryBucket } from '@/api/types'
import {
  dayLabel,
  hourLabel,
  hourRangeLabel,
  isLocalMidnight,
  niceCeil,
  topRoundedRect,
  yTicks,
} from '@/components/dashboard/chart'
import { formatCount } from '@/components/dashboard/format'
import Skeleton from '@/components/ui/Skeleton.vue'

const props = defineProps<{
  /** Hourly buckets oldest→newest; null while the first load is in flight. */
  buckets: MetricsHistoryBucket[] | null
}>()

const PLOT_H = 160
const SEG_GAP = 2 // air between the success and error segments
const CAP_R = 3 // rounded top-cap radius
const MAX_BAR_W = 24 // wider bars read as blocks

const plotEl = ref<HTMLElement | null>(null)
const plotW = ref(0)
let ro: ResizeObserver | undefined
onMounted(() => {
  ro = new ResizeObserver((entries) => {
    plotW.value = entries[0]?.contentRect.width ?? 0
  })
  if (plotEl.value) ro.observe(plotEl.value)
})
onBeforeUnmount(() => ro?.disconnect())

const totals = computed(() => {
  let success = 0
  let errors = 0
  for (const b of props.buckets ?? []) {
    success += b.success
    errors += b.errors
  }
  return { success, errors }
})
const empty = computed(() => totals.value.success + totals.value.errors === 0)

const yMax = computed(() =>
  niceCeil(Math.max(0, ...(props.buckets ?? []).map((b) => b.success + b.errors))),
)
const gridlines = computed(() =>
  yTicks(yMax.value).map((v) => ({ v, y: PLOT_H - (v / yMax.value) * PLOT_H })),
)

interface BarSeg {
  d: string
  kind: 'ok' | 'err'
}
interface Bar {
  i: number
  bandX: number
  bandW: number
  segs: BarSeg[]
}

const bars = computed<Bar[]>(() => {
  const bs = props.buckets ?? []
  if (bs.length === 0 || plotW.value <= 0) return []
  const band = plotW.value / bs.length
  const barW = Math.min(MAX_BAR_W, Math.max(3, band - 2))
  const scale = PLOT_H / yMax.value
  return bs.map((b, i) => {
    const x = i * band + (band - barW) / 2
    // Non-zero values always get >=1px so a lone error is never invisible.
    const okH = b.success > 0 ? Math.max(1, b.success * scale) : 0
    const errH = b.errors > 0 ? Math.max(1, b.errors * scale) : 0
    const gap = okH > 0 && errH > 0 ? SEG_GAP : 0
    const segs: BarSeg[] = []
    if (okH > 0) {
      const y = PLOT_H - okH
      // Rounded cap only when success is the top segment.
      segs.push({
        d:
          errH > 0
            ? `M${x},${y} h${barW} v${okH} h${-barW} Z`
            : topRoundedRect(x, y, barW, okH, CAP_R),
        kind: 'ok',
      })
    }
    if (errH > 0) {
      segs.push({
        d: topRoundedRect(x, Math.max(0, PLOT_H - okH - gap - errH), barW, errH, CAP_R),
        kind: 'err',
      })
    }
    return { i, bandX: i * band, bandW: band, segs }
  })
})

// Ticks align to LOCAL wall-clock hours (00/06/12/18) so every local
// midnight is a tick; midnights label the DATE instead of the time.
const xTicks = computed(() => {
  const bs = props.buckets ?? []
  if (bs.length === 0 || plotW.value <= 0) return []
  const band = plotW.value / bs.length
  const edge = 24 // px: ticks this close to a plot edge align inward
  return bs
    .map((b, i) => {
      const left = (i + 0.5) * band
      const shift =
        left < edge ? '' : left > plotW.value - edge ? '-translate-x-full' : '-translate-x-1/2'
      const midnight = isLocalMidnight(b.start)
      return {
        i,
        midnight,
        label: midnight ? dayLabel(b.start) : hourLabel(b.start),
        left,
        shift,
        localHour: new Date(b.start).getHours(),
      }
    })
    .filter((t) => t.localHour % 6 === 0)
})

const hovered = ref<number | null>(null)
const tooltip = computed(() => {
  const bs = props.buckets ?? []
  if (hovered.value === null || plotW.value <= 0) return null
  const b = bs[hovered.value]
  if (b === undefined) return null
  const band = plotW.value / bs.length
  const center = (hovered.value + 0.5) * band
  return {
    range: `${dayLabel(b.start)} ${hourRangeLabel(b.start)}`,
    success: formatCount(b.success),
    errors: formatCount(b.errors),
    left: Math.min(Math.max(center, 90), plotW.value - 90),
  }
})

const ariaLabel = computed(
  () =>
    `Last 48 hours: ${formatCount(totals.value.success)} successful requests, ` +
    `${formatCount(totals.value.errors)} errors`,
)
</script>

<template>
  <div class="glass-panel p-4">
    <div class="flex flex-wrap items-baseline justify-between gap-x-4 gap-y-1">
      <p class="text-[11px] font-medium uppercase tracking-widest text-fg-muted">Traffic · 48h</p>
      <div v-if="buckets !== null" class="flex items-center gap-4 text-xs">
        <span class="flex items-center gap-1.5">
          <span class="h-2.5 w-2.5 rounded-sm" style="background: var(--color-success)" />
          <span class="text-fg-secondary">OK</span>
          <span class="font-mono tnums text-fg">{{ formatCount(totals.success) }}</span>
        </span>
        <span class="flex items-center gap-1.5">
          <span class="h-2.5 w-2.5 rounded-sm" style="background: var(--color-danger)" />
          <span class="text-fg-secondary">Errors</span>
          <span class="font-mono tnums text-fg">{{ formatCount(totals.errors) }}</span>
        </span>
      </div>
    </div>

    <div v-if="buckets === null" class="mt-3" aria-hidden="true">
      <Skeleton class="h-[160px] w-full" />
      <Skeleton class="mt-2 h-3 w-3/4" />
    </div>

    <div v-else class="mt-3">
      <div ref="plotEl" class="relative" :style="{ height: `${PLOT_H}px` }">
        <svg
          v-if="plotW > 0"
          :viewBox="`0 0 ${plotW} ${PLOT_H}`"
          :width="plotW"
          :height="PLOT_H"
          class="absolute inset-0"
          role="img"
          :aria-label="ariaLabel"
          @mouseleave="hovered = null"
        >
          <line
            v-for="g in gridlines"
            :key="g.v"
            :x1="0"
            :x2="plotW"
            :y1="g.y"
            :y2="g.y"
            stroke="var(--color-border)"
            stroke-width="1"
          />
          <template v-for="bar in bars" :key="bar.i">
            <rect
              v-if="hovered === bar.i"
              :x="bar.bandX"
              y="0"
              :width="bar.bandW"
              :height="PLOT_H"
              style="fill: var(--color-fg)"
              opacity="0.05"
            />
            <path
              v-for="(seg, si) in bar.segs"
              :key="si"
              :d="seg.d"
              :style="{
                fill: seg.kind === 'ok' ? 'var(--color-success)' : 'var(--color-danger)',
              }"
              :opacity="hovered === null || hovered === bar.i ? 0.92 : 0.45"
            />
            <rect
              :x="bar.bandX"
              y="0"
              :width="bar.bandW"
              :height="PLOT_H"
              fill="transparent"
              @mouseenter="hovered = bar.i"
            />
          </template>
        </svg>

        <!-- Text stays HTML so it wears the text tokens and never scales. -->
        <span
          v-for="g in gridlines"
          :key="`label-${g.v}`"
          class="pointer-events-none absolute right-0 font-mono text-[10px] tnums text-fg-muted"
          :style="{ top: `${Math.max(0, g.y - 14)}px` }"
        >
          {{ formatCount(g.v) }}
        </span>

        <p
          v-if="empty"
          class="pointer-events-none absolute inset-0 flex items-center justify-center text-xs text-fg-muted"
        >
          No traffic in the last 48 hours
        </p>

        <div
          v-if="tooltip"
          class="pointer-events-none absolute top-1 z-10 -translate-x-1/2 rounded-lg border border-border bg-surface-raised px-2.5 py-1.5 text-xs shadow-lg"
          :style="{ left: `${tooltip.left}px` }"
        >
          <p class="font-mono tnums text-fg-secondary">{{ tooltip.range }}</p>
          <div class="mt-1 space-y-0.5">
            <p class="flex items-center gap-1.5">
              <span class="h-2 w-2 rounded-sm" style="background: var(--color-success)" />
              <span class="text-fg-muted">OK</span>
              <span class="ml-auto pl-3 font-mono font-semibold tnums text-fg">{{
                tooltip.success
              }}</span>
            </p>
            <p class="flex items-center gap-1.5">
              <span class="h-2 w-2 rounded-sm" style="background: var(--color-danger)" />
              <span class="text-fg-muted">Errors</span>
              <span class="ml-auto pl-3 font-mono font-semibold tnums text-fg">{{
                tooltip.errors
              }}</span>
            </p>
          </div>
        </div>
      </div>

      <div class="relative mt-1.5 h-4 font-mono text-[10px] tnums text-fg-muted">
        <span
          v-for="t in xTicks"
          :key="t.i"
          class="absolute whitespace-nowrap"
          :class="[t.shift, t.midnight ? 'font-medium text-fg-secondary' : '']"
          :style="{ left: `${t.left}px` }"
        >
          {{ t.label }}
        </span>
      </div>
    </div>
  </div>
</template>
