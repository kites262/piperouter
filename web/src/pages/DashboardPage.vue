<!-- Dashboard (PRD §19.1) — judge PipeRouter health within 5 seconds. -->
<script setup lang="ts">
import { RotateCw, ServerCrash, TriangleAlert } from 'lucide-vue-next'
import { computed, ref } from 'vue'

import { ApiError, getLogs, getMetrics, getStatus, listRoutes } from '@/api/client'
import type { AccessLogEntry, MetricsSnapshot, RouteMetrics, StatusResponse } from '@/api/types'
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
import Button from '@/components/ui/Button.vue'
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
  dot?: 'success' | 'warning' | 'danger' | 'muted'
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
      sub: `SSE ${formatCount(m.active_sse)} · WS ${formatCount(m.active_websockets)}`,
      tone: 'default',
      mono: true,
    },
    {
      key: 'total',
      label: 'Total requests',
      value: formatCount(total),
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
    <header class="flex items-end justify-between gap-4">
      <div>
        <h1 class="text-lg font-semibold text-fg">Dashboard</h1>
        <p class="mt-1 text-sm text-fg-muted">Service health, traffic and latency at a glance.</p>
      </div>
      <span class="hidden shrink-0 font-mono text-[11px] text-fg-muted sm:block">
        auto-refresh · 3s
      </span>
    </header>

    <!-- Poll failing but stale data still on screen -->
    <div
      v-if="staleWarning"
      class="flex items-center gap-2 rounded-lg border border-warning/30 bg-warning-soft px-3 py-2 text-xs text-warning"
      role="alert"
    >
      <TriangleAlert class="h-3.5 w-3.5 shrink-0" />
      <span class="min-w-0 truncate">
        Live data unavailable ({{ loadError }}) — retrying. Showing last known values.
      </span>
    </div>

    <!-- Initial loading skeleton -->
    <template v-if="initialLoading">
      <div class="grid grid-cols-1 gap-4 sm:grid-cols-2 xl:grid-cols-4" aria-hidden="true">
        <div v-for="i in 8" :key="i" class="glass rounded-xl p-4">
          <div class="h-3 w-20 animate-pulse rounded bg-surface-raised" />
          <div class="mt-3 h-6 w-24 animate-pulse rounded bg-surface-raised" />
          <div class="mt-2.5 h-3 w-28 animate-pulse rounded bg-surface-raised" />
        </div>
      </div>
      <div class="grid items-start gap-6 xl:grid-cols-5">
        <div class="xl:col-span-3"><RoutesOverviewTable :routes="[]" :prefixes="{}" loading /></div>
        <div class="xl:col-span-2"><RecentErrorsPanel :entries="[]" loading /></div>
      </div>
    </template>

    <!-- Admin API unreachable before any data arrived -->
    <div
      v-else-if="!hasData"
      class="card-flat flex flex-col items-center gap-3 px-6 py-14 text-center"
    >
      <div class="flex h-10 w-10 items-center justify-center rounded-lg bg-danger-soft text-danger">
        <ServerCrash class="h-5 w-5" />
      </div>
      <p class="text-sm font-medium text-fg">Cannot reach the admin API</p>
      <p class="max-w-md break-all font-mono text-xs text-fg-muted">{{ loadError }}</p>
      <Button variant="outline" size="sm" @click="retry">
        <RotateCw class="h-3.5 w-3.5" />
        Retry
      </Button>
    </div>

    <!-- Live dashboard -->
    <template v-else>
      <div class="grid grid-cols-1 gap-4 sm:grid-cols-2 xl:grid-cols-4">
        <StatCard
          v-for="c in cards"
          :key="c.key"
          :label="c.label"
          :value="c.value"
          :sub="c.sub"
          :tone="c.tone"
          :mono="c.mono"
          :dot="c.dot"
        />
      </div>

      <div class="grid items-start gap-6 xl:grid-cols-5">
        <div class="xl:col-span-3">
          <RoutesOverviewTable :routes="routeMetrics" :prefixes="prefixes" />
        </div>
        <div class="xl:col-span-2">
          <RecentErrorsPanel :entries="errorEntries" />
        </div>
      </div>
    </template>
  </section>
</template>
