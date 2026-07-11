<!-- Dashboard (PRD §19.1) — judge PipeRouter health within 5 seconds. -->
<script setup lang="ts">
import { RotateCw, ServerCrash, TriangleAlert } from 'lucide-vue-next'
import { computed, ref } from 'vue'

import { ApiError, getLogs, getMetrics, getMetricsHistory, getStatus, listRoutes } from '@/api/client'
import type {
  AccessLogEntry,
  MetricsHistoryResponse,
  MetricsSnapshot,
  RouteMetrics,
  StatusResponse,
} from '@/api/types'
import {
  formatCount,
  formatErrorRate,
  formatMs,
  formatRelativeTime,
  formatUptime,
  shortRevision,
} from '@/components/dashboard/format'
import RecentErrorsPanel from '@/components/dashboard/RecentErrorsPanel.vue'
import RoutesOverviewTable from '@/components/dashboard/RoutesOverviewTable.vue'
import StatCard from '@/components/dashboard/StatCard.vue'
import TrafficHistoryChart from '@/components/dashboard/TrafficHistoryChart.vue'
import Button from '@/components/ui/Button.vue'
import Skeleton from '@/components/ui/Skeleton.vue'
import { usePolling } from '@/composables/usePolling'
import { useToast } from '@/composables/useToast'

const { error: toastError } = useToast()

const status = ref<StatusResponse | null>(null)
const metrics = ref<MetricsSnapshot | null>(null)
const prefixes = ref<Record<string, string>>({})
const errorEntries = ref<AccessLogEntry[]>([])
/** Last poll failure ("" never — null when healthy). Data stays stale on failure. */
const loadError = ref<string | null>(null)

async function fetchAll(): Promise<void> {
  try {
    const [st, m, routes] = await Promise.all([getStatus(), getMetrics(), listRoutes()])
    // Recent errors: error-classified entries first; fall back to plain 5xx
    // responses (upstream-origin errors carry no error code).
    let entries = (await getLogs({ limit: 20, status_class: 'error' })).entries ?? []
    if (entries.length === 0) {
      entries = (await getLogs({ limit: 20, status_class: '5xx' })).entries ?? []
    }
    status.value = st
    metrics.value = m
    prefixes.value = Object.fromEntries(routes.map((r) => [r.name, r.prefix]))
    errorEntries.value = entries
    loadError.value = null
  } catch (err) {
    const message =
      err instanceof ApiError ? (err.detail !== '' ? err.detail : err.code) : String(err)
    // Toast only on the healthy → failing transition, never every 3s.
    if (loadError.value === null) {
      toastError('Failed to load dashboard', { message })
    }
    loadError.value = message
  }
}

const { refresh } = usePolling(fetchAll, { interval: 3000 })

/** 24h history — hourly buckets change slowly, so it polls on its own 30s
 * cadence. Failures keep the last series silently (the 3s poll already
 * owns the stale-data banner). */
const history = ref<MetricsHistoryResponse | null>(null)

async function fetchHistory(): Promise<void> {
  try {
    history.value = await getMetricsHistory()
  } catch {
    // keep last known series
  }
}

usePolling(fetchHistory, { interval: 30000 })

function retry(): void {
  void refresh()
}

const hasData = computed(() => status.value !== null && metrics.value !== null)
const initialLoading = computed(() => !hasData.value && loadError.value === null)
const staleWarning = computed(() => hasData.value && loadError.value !== null)

const routeMetrics = computed<RouteMetrics[]>(() => metrics.value?.routes ?? [])

interface StatCardModel {
  key: string
  label: string
  value: string
  sub: string
  tone: 'default' | 'success' | 'warning' | 'danger'
  mono: boolean
  dot?: 'success' | 'warning' | 'danger' | 'accent' | 'muted'
  /** When set, the tile tweens to this number on change. */
  animate?: number
  format?: (n: number) => string
}

const cards = computed<StatCardModel[]>(() => {
  const st = status.value
  const m = metrics.value
  if (st === null || m === null) return []

  const unreachable = loadError.value !== null
  const cfg = st.config
  const total = m.total_requests
  const errors = m.error_requests
  const lat = m.latency
  const errorPct = total > 0 ? (errors / total) * 100 : 0

  return [
    {
      key: 'service',
      label: 'Service',
      value: unreachable ? 'Unreachable' : 'Running',
      sub: `up ${formatUptime(st.uptime_seconds)}`,
      tone: unreachable ? 'danger' : 'default',
      mono: false,
      dot: unreachable ? 'danger' : 'success',
    },
    {
      key: 'config',
      label: 'Config',
      value: cfg.valid ? shortRevision(cfg.revision) : 'Invalid',
      sub: cfg.valid
        ? `loaded ${formatRelativeTime(cfg.loaded_at)}`
        : cfg.last_error !== ''
          ? cfg.last_error
          : 'configuration failed to load',
      tone: cfg.valid ? 'default' : 'danger',
      mono: cfg.valid,
      dot: cfg.valid ? 'success' : 'danger',
    },
    {
      key: 'routes',
      label: 'Routes',
      value: String(m.route_count),
      sub: 'enabled routes',
      tone: 'default',
      mono: true,
    },
    {
      key: 'transports',
      label: 'Transports',
      value: String(m.transport_count),
      sub: 'including built-in direct',
      tone: 'default',
      mono: true,
    },
    {
      key: 'active',
      label: 'Active requests',
      value: formatCount(m.active_requests),
      animate: m.active_requests,
      format: (n) => formatCount(Math.round(n)),
      sub: `SSE ${formatCount(m.active_sse)} · WS ${formatCount(m.active_websockets)}`,
      tone: 'default',
      mono: true,
      // Live accent pulse whenever traffic is in flight.
      dot: m.active_requests > 0 ? 'accent' : undefined,
    },
    {
      key: 'total',
      label: 'Total requests',
      value: formatCount(total),
      animate: total,
      format: (n) => formatCount(Math.round(n)),
      sub: total === 0 ? 'no traffic yet' : `${formatCount(errors)} error${errors === 1 ? '' : 's'}`,
      tone: 'default',
      mono: true,
    },
    {
      key: 'error-rate',
      label: 'Error rate',
      value: total === 0 ? '—' : formatErrorRate(errors, total),
      sub:
        total === 0
          ? 'no requests yet'
          : `${formatCount(errors)} of ${formatCount(total)} requests`,
      tone: total === 0 ? 'default' : errors === 0 ? 'success' : errorPct >= 5 ? 'danger' : 'warning',
      mono: true,
    },
    {
      key: 'p95',
      label: 'P95 latency',
      value: lat.count > 0 ? formatMs(lat.p95_ms) : '—',
      sub:
        lat.count > 0
          ? `P50 ${formatMs(lat.p50_ms)} · P99 ${formatMs(lat.p99_ms)}`
          : 'no samples yet',
      tone: 'default',
      mono: true,
    },
  ]
})
</script>

<template>
  <section class="space-y-6">
    <header class="flex items-end justify-between gap-4 animate-fade-up">
      <div>
        <h1 class="text-lg font-semibold tracking-tight text-fg">Dashboard</h1>
        <p class="mt-1 text-sm text-fg-muted">Service health, traffic and latency at a glance.</p>
      </div>
      <span
        class="hidden shrink-0 rounded-full border border-border px-2.5 py-1 font-mono text-[11px] text-fg-muted sm:block"
        style="background: var(--toolbar-bg)"
      >
        auto-refresh · 3s
      </span>
    </header>

    <div
      v-if="staleWarning"
      class="flex items-center gap-2 rounded-xl border border-warning/30 bg-warning-soft px-3 py-2 text-xs text-warning"
      role="alert"
    >
      <TriangleAlert class="h-3.5 w-3.5 shrink-0" />
      <span class="min-w-0 truncate">
        Live data unavailable ({{ loadError }}) — retrying. Showing last known values.
      </span>
    </div>

    <template v-if="initialLoading">
      <div class="grid grid-cols-1 gap-4 sm:grid-cols-2 xl:grid-cols-4" aria-hidden="true">
        <div v-for="i in 8" :key="i" class="glass-panel p-4">
          <Skeleton class="h-3 w-20" />
          <Skeleton class="mt-3 h-6 w-24" />
          <Skeleton class="mt-2.5 h-3 w-28" />
        </div>
      </div>
      <div class="glass-panel p-4" aria-hidden="true">
        <Skeleton class="h-3 w-24" />
        <Skeleton class="mt-3 h-[160px] w-full" />
        <Skeleton class="mt-2 h-3 w-3/4" />
      </div>
      <div class="grid items-start gap-6 xl:grid-cols-5">
        <div class="xl:col-span-3"><RoutesOverviewTable :routes="[]" :prefixes="{}" loading /></div>
        <div class="xl:col-span-2"><RecentErrorsPanel :entries="[]" loading /></div>
      </div>
    </template>

    <div
      v-else-if="!hasData"
      class="glass-panel flex flex-col items-center gap-3 px-6 py-14 text-center"
    >
      <div
        class="flex h-11 w-11 items-center justify-center rounded-xl border border-danger/25 bg-danger-soft text-danger"
      >
        <ServerCrash class="h-5 w-5" />
      </div>
      <p class="text-sm font-medium text-fg">Cannot reach the admin API</p>
      <p class="max-w-md break-all font-mono text-xs text-fg-muted">{{ loadError }}</p>
      <Button variant="outline" size="sm" @click="retry">
        <RotateCw class="h-3.5 w-3.5" />
        Retry
      </Button>
    </div>

    <template v-else>
      <div class="grid grid-cols-1 gap-4 sm:grid-cols-2 xl:grid-cols-4">
        <StatCard
          v-for="(c, i) in cards"
          :key="c.key"
          class="animate-fade-up"
          :class="`stagger-${Math.min(i + 1, 8)}`"
          :label="c.label"
          :value="c.value"
          :sub="c.sub"
          :tone="c.tone"
          :mono="c.mono"
          :dot="c.dot"
          :animate="c.animate"
          :format="c.format"
        />
      </div>

      <TrafficHistoryChart class="animate-fade-up stagger-6" :buckets="history?.buckets ?? null" />

      <div class="grid items-start gap-6 xl:grid-cols-5">
        <div class="animate-fade-up stagger-7 xl:col-span-3">
          <RoutesOverviewTable :routes="routeMetrics" :prefixes="prefixes" />
        </div>
        <div class="animate-fade-up stagger-8 xl:col-span-2">
          <RecentErrorsPanel :entries="errorEntries" />
        </div>
      </div>
    </template>
  </section>
</template>
