<script setup lang="ts">
import { ArrowLeft, FlaskConical, Info, RefreshCw, SearchX } from 'lucide-vue-next'
import { computed, ref, watch } from 'vue'
import { RouterLink, useRoute } from 'vue-router'

import { ApiError, getLogs, getRoute, getRouteMetrics, listTransports } from '@/api/client'
import type { AccessLogEntry, RouteConfig, RouteMetrics } from '@/api/types'
import PipelineViz from '@/components/routes/PipelineViz.vue'
import RouteTestDialog from '@/components/routes/RouteTestDialog.vue'
import Badge from '@/components/ui/Badge.vue'
import Button from '@/components/ui/Button.vue'
import Card from '@/components/ui/Card.vue'
import EmptyState from '@/components/ui/EmptyState.vue'
import Spinner from '@/components/ui/Spinner.vue'
import Table from '@/components/ui/Table.vue'
import TBody from '@/components/ui/TBody.vue'
import Td from '@/components/ui/Td.vue'
import Th from '@/components/ui/Th.vue'
import THead from '@/components/ui/THead.vue'
import Tr from '@/components/ui/Tr.vue'
import { usePolling } from '@/composables/usePolling'

const route = useRoute()
const name = computed(() => String(route.params.name ?? ''))

// --- route config (one-shot load; 404 → friendly not-found) ---------------

const routeCfg = ref<RouteConfig | null>(null)
const loading = ref(true)
const notFound = ref(false)
const loadError = ref('')
const transportType = ref('')
const testOpen = ref(false)

function errMessage(err: unknown): string {
  return err instanceof ApiError ? (err.detail !== '' ? err.detail : err.code) : String(err)
}

async function loadRoute(): Promise<void> {
  loading.value = true
  notFound.value = false
  loadError.value = ''
  try {
    routeCfg.value = await getRoute(name.value)
    void loadTransportType()
  } catch (err) {
    routeCfg.value = null
    if (err instanceof ApiError && err.status === 404) {
      notFound.value = true
    } else {
      loadError.value = errMessage(err)
    }
  } finally {
    loading.value = false
  }
}

/** Best-effort lookup of the transport's type for the pipeline diagram. */
async function loadTransportType(): Promise<void> {
  const wanted = routeCfg.value?.transport ?? ''
  if (wanted === '') return
  try {
    const transports = await listTransports()
    transportType.value = transports.find((t) => t.name === wanted)?.type ?? ''
  } catch {
    transportType.value = ''
  }
}

// --- route metrics (poll every 3s) -----------------------------------------

const metrics = ref<RouteMetrics | null>(null)
const metricsError = ref('')

async function fetchMetrics(): Promise<void> {
  if (routeCfg.value === null || name.value === '') return
  try {
    metrics.value = await getRouteMetrics(name.value)
    metricsError.value = ''
  } catch (err) {
    metricsError.value = errMessage(err)
  }
}

const metricsPoll = usePolling(fetchMetrics, { interval: 3000, immediate: false })

// --- recent requests for this route (poll every 5s) ------------------------

const logEntries = ref<AccessLogEntry[] | null>(null)
const logsError = ref('')

async function fetchLogs(): Promise<void> {
  if (routeCfg.value === null || name.value === '') return
  try {
    const res = await getLogs({ route: name.value, limit: 20 })
    logEntries.value = res.entries ?? []
    logsError.value = ''
  } catch (err) {
    logsError.value = errMessage(err)
  }
}

const logsPoll = usePolling(fetchLogs, { interval: 5000 })

// --- load + reload when the :name param changes -----------------------------

async function initialize(): Promise<void> {
  testOpen.value = false
  metrics.value = null
  metricsError.value = ''
  logEntries.value = null
  logsError.value = ''
  transportType.value = ''
  await loadRoute()
  if (routeCfg.value !== null) {
    void metricsPoll.refresh()
    void logsPoll.refresh()
  }
}

watch(
  name,
  () => {
    if (name.value === '') return
    void initialize()
  },
  { immediate: true },
)

// --- derived view data ------------------------------------------------------

const configFields = computed(() => {
  const r = routeCfg.value
  if (r === null) return []
  return [
    { label: 'Name', value: r.name },
    { label: 'Prefix', value: r.prefix },
    { label: 'Target', value: r.target },
    {
      label: 'Transport',
      value:
        transportType.value !== '' && transportType.value !== r.transport
          ? `${r.transport} (${transportType.value})`
          : r.transport,
    },
    { label: 'Strip prefix', value: r.strip_prefix ? 'true' : 'false' },
    { label: 'Strip forward headers', value: r.strip_forward_headers ? 'true' : 'false' },
    { label: 'Enabled', value: r.enabled ? 'true' : 'false' },
  ]
})

/**
 * Error rate per the backend definition (metrics.Observe): a request is an
 * error when status >= 500 OR the upstream failed. Upstream failures are
 * mapped to 502/504 (PRD §9.6) and therefore already counted in status_5xx —
 * max() avoids double counting while still covering any upstream error that
 * was recorded without a 5xx status.
 */
const errorRate = computed(() => {
  const m = metrics.value
  if (m === null || m.total === 0) return 0
  return Math.min(1, Math.max(m.status_5xx, m.upstream_errors) / m.total)
})

const errorTone = computed(() => {
  if (errorRate.value === 0) return 'text-fg'
  return errorRate.value < 0.05 ? 'text-warning' : 'text-danger'
})

const stats = computed(() => {
  const m = metrics.value
  return [
    { label: 'Requests', value: m !== null ? fmtCount(m.total) : '—', tone: 'text-fg' },
    {
      label: 'Error rate',
      value: m !== null ? `${(errorRate.value * 100).toFixed(1)}%` : '—',
      tone: errorTone.value,
    },
    { label: 'Active', value: m !== null ? fmtCount(m.active) : '—', tone: 'text-fg' },
    { label: 'P50', value: m !== null ? fmtMs(m.latency.p50_ms) : '—', tone: 'text-fg' },
    { label: 'P95', value: m !== null ? fmtMs(m.latency.p95_ms) : '—', tone: 'text-fg' },
    { label: 'P99', value: m !== null ? fmtMs(m.latency.p99_ms) : '—', tone: 'text-fg' },
  ]
})

interface BreakdownRow {
  key: string
  label: string
  count: number
  pct: number
  cls: string
  striped: boolean
}

const breakdown = computed<BreakdownRow[]>(() => {
  const m = metrics.value
  if (m === null) return []
  const total = Math.max(m.total, 1)
  const rows = [
    { key: '2xx', label: '2xx', count: m.status_2xx, cls: 'bg-success', striped: false },
    { key: '3xx', label: '3xx', count: m.status_3xx, cls: 'bg-accent', striped: false },
    { key: '4xx', label: '4xx', count: m.status_4xx, cls: 'bg-warning', striped: false },
    { key: '5xx', label: '5xx', count: m.status_5xx, cls: 'bg-danger', striped: false },
    {
      key: 'upstream',
      label: 'Upstream errors',
      count: m.upstream_errors,
      cls: '',
      striped: true,
    },
  ]
  return rows.map((r) => ({ ...r, pct: (r.count / total) * 100 }))
})

function barStyle(row: BreakdownRow): Record<string, string> {
  const style: Record<string, string> = { width: `${Math.min(100, row.pct)}%` }
  if (row.striped) {
    style.background =
      'repeating-linear-gradient(45deg, var(--color-danger), var(--color-danger) 4px, rgb(248 113 113 / 0.45) 4px, rgb(248 113 113 / 0.45) 8px)'
  }
  return style
}

// --- formatting helpers (all timestamps rendered locally) -------------------

function fmtCount(n: number): string {
  return n.toLocaleString()
}

function fmtMs(ms: number): string {
  if (!Number.isFinite(ms)) return '—'
  if (ms >= 1000) return `${(ms / 1000).toFixed(2)} s`
  if (ms >= 100) return `${Math.round(ms)} ms`
  return `${ms.toFixed(1)} ms`
}

function fmtTime(t: string): string {
  const d = new Date(t)
  return Number.isNaN(d.getTime()) ? t : d.toLocaleTimeString(undefined, { hour12: false })
}

function fmtDateTime(t: string): string {
  const d = new Date(t)
  return Number.isNaN(d.getTime()) ? t : d.toLocaleString()
}

function statusVariant(status: number): 'success' | 'accent' | 'warning' | 'danger' | 'muted' {
  if (status >= 200 && status < 300) return 'success'
  if (status >= 300 && status < 400) return 'accent'
  if (status >= 400 && status < 500) return 'warning'
  if (status >= 500) return 'danger'
  return 'muted'
}
</script>

<template>
  <section class="space-y-6">
    <div class="flex flex-wrap items-start justify-between gap-4 animate-fade-up">
      <div class="min-w-0">
        <RouterLink
          to="/routes"
          class="inline-flex items-center gap-1.5 text-sm text-fg-secondary transition-colors duration-150 hover:text-fg"
        >
          <ArrowLeft class="h-4 w-4" />
          Routes
        </RouterLink>
        <div class="mt-1 flex flex-wrap items-center gap-3">
          <h1 class="truncate font-mono text-lg font-semibold tracking-tight text-fg">{{ name }}</h1>
          <Badge v-if="routeCfg" :variant="routeCfg.enabled ? 'success' : 'muted'">
            {{ routeCfg.enabled ? 'enabled' : 'disabled' }}
          </Badge>
        </div>
        <p class="mt-1 text-sm text-fg-muted">Pipeline, configuration, live metrics and route testing.</p>
      </div>
      <Button v-if="routeCfg" @click="testOpen = true">
        <FlaskConical class="h-4 w-4" />
        Test Route
      </Button>
    </div>

    <div v-if="loading" class="space-y-6" aria-busy="true">
      <div class="glass-panel h-28 animate-pulse" />
      <div class="glass-panel h-40 animate-pulse" />
      <div class="grid grid-cols-2 gap-3 sm:grid-cols-3 xl:grid-cols-6">
        <div v-for="i in 6" :key="i" class="glass-panel h-[4.5rem] animate-pulse" />
      </div>
      <div class="card-flat h-64 animate-pulse" />
    </div>

    <!-- 404 — friendly not-found -->
    <EmptyState
      v-else-if="notFound"
      title="Route not found"
      :description="`No route named “${name}” exists in the current configuration. It may have been renamed or deleted.`"
    >
      <template #icon><SearchX class="h-5 w-5" /></template>
      <RouterLink
        to="/routes"
        class="inline-flex h-9 items-center gap-1.5 rounded-md border border-border-strong px-3.5 text-sm font-medium text-fg transition-colors duration-150 hover:bg-surface-raised"
      >
        <ArrowLeft class="h-4 w-4" />
        Back to Routes
      </RouterLink>
    </EmptyState>

    <div
      v-else-if="loadError"
      class="glass-panel flex flex-wrap items-center justify-between gap-3 border-danger/30 px-4 py-3"
    >
      <p class="text-sm text-danger">Failed to load route: {{ loadError }}</p>
      <Button variant="outline" size="sm" @click="initialize">
        <RefreshCw class="h-3.5 w-3.5" />
        Retry
      </Button>
    </div>

    <template v-else-if="routeCfg">
      <Card glass animate title="Pipeline">
        <PipelineViz :route="routeCfg" :transport-type="transportType || undefined" />
      </Card>

      <Card glass class="animate-fade-up stagger-2" title="Configuration">
        <dl class="grid grid-cols-1 gap-x-8 gap-y-4 sm:grid-cols-2 lg:grid-cols-3">
          <div v-for="field in configFields" :key="field.label">
            <dt class="text-xs font-medium uppercase tracking-wide text-fg-muted">{{ field.label }}</dt>
            <dd class="mt-1 break-all font-mono text-sm text-fg">{{ field.value }}</dd>
          </div>
        </dl>
        <p class="mt-4 flex flex-wrap items-center gap-1.5 text-xs text-fg-muted">
          <Info class="h-3.5 w-3.5 shrink-0" />
          Read-only view — edit this route from the
          <RouterLink to="/routes" class="text-accent hover:underline">Routes page</RouterLink>.
        </p>
      </Card>

      <section class="animate-fade-up stagger-3">
        <div class="mb-3 flex flex-wrap items-center justify-between gap-3">
          <h2 class="text-sm font-semibold tracking-tight text-fg">Live metrics</h2>
          <span v-if="metricsError" class="text-xs text-danger">{{ metricsError }}</span>
          <span v-else-if="metrics?.last_request_at" class="font-mono text-xs text-fg-muted">
            last request {{ fmtDateTime(metrics.last_request_at) }}
          </span>
          <span v-else-if="metrics" class="font-mono text-xs text-fg-muted">no requests yet</span>
        </div>
        <div class="grid grid-cols-2 gap-3 sm:grid-cols-3 xl:grid-cols-6">
          <div
            v-for="stat in stats"
            :key="stat.label"
            class="glass-panel card-lift px-4 py-3"
          >
            <p class="text-[11px] font-medium uppercase tracking-wide text-fg-muted">{{ stat.label }}</p>
            <p class="mt-1 truncate font-mono text-lg tnums" :class="stat.tone">{{ stat.value }}</p>
          </div>
        </div>
      </section>

      <Card class="animate-fade-up stagger-4" title="Status class breakdown">
        <div v-if="metrics === null" class="space-y-3" aria-busy="true">
          <div v-for="i in 5" :key="i" class="h-4 animate-pulse rounded bg-surface-raised/70" />
        </div>
        <div v-else class="space-y-3">
          <div v-for="row in breakdown" :key="row.key" class="flex items-center gap-3">
            <span class="w-28 shrink-0 text-xs text-fg-secondary">{{ row.label }}</span>
            <div class="h-2 min-w-0 flex-1 overflow-hidden rounded-full bg-surface-raised">
              <div
                class="h-full rounded-full transition-[width] duration-200"
                :class="row.cls"
                :style="barStyle(row)"
              />
            </div>
            <span class="w-28 shrink-0 text-right font-mono text-xs text-fg-secondary">
              {{ fmtCount(row.count) }} · {{ row.pct.toFixed(1) }}%
            </span>
          </div>
        </div>
      </Card>

      <!-- Recent requests -->
      <section>
        <div class="mb-3 flex flex-wrap items-center justify-between gap-3">
          <h2 class="text-sm font-semibold text-fg">Recent requests</h2>
          <span v-if="logsError" class="text-xs text-danger">{{ logsError }}</span>
        </div>
        <Table>
          <THead>
            <tr>
              <Th>Time</Th>
              <Th>Method</Th>
              <Th>Path</Th>
              <Th>Status</Th>
              <Th>Duration</Th>
              <Th>Transport</Th>
              <Th>Error</Th>
            </tr>
          </THead>
          <TBody>
            <Tr v-if="logEntries === null && logsError === ''">
              <Td colspan="7">
                <div class="flex items-center justify-center gap-2 py-6 text-fg-muted">
                  <Spinner size="sm" />
                  Loading recent requests…
                </div>
              </Td>
            </Tr>
            <Tr v-else-if="logEntries === null || logEntries.length === 0">
              <Td colspan="7" class="py-8 text-center text-fg-muted">
                No recent requests for this route.
              </Td>
            </Tr>
            <template v-else>
              <Tr v-for="(entry, i) in logEntries" :key="`${entry.time}-${i}`">
                <Td class="font-mono text-xs">{{ fmtTime(entry.time) }}</Td>
                <Td class="font-mono text-xs">{{ entry.method }}</Td>
                <Td class="max-w-64">
                  <div class="flex min-w-0 items-center gap-2">
                    <span class="truncate font-mono text-xs" :title="entry.path">{{ entry.path }}</span>
                    <Badge v-if="entry.streaming !== ''" variant="accent">
                      {{ entry.streaming === 'websocket' ? 'ws' : entry.streaming }}
                    </Badge>
                  </div>
                </Td>
                <Td>
                  <Badge :variant="statusVariant(entry.status)" mono>{{ entry.status }}</Badge>
                </Td>
                <Td class="font-mono text-xs">{{ fmtMs(entry.duration_ms) }}</Td>
                <Td class="font-mono text-xs">{{ entry.transport }}</Td>
                <Td>
                  <span v-if="entry.error !== ''" class="font-mono text-xs text-danger">{{ entry.error }}</span>
                  <span v-else class="text-fg-muted">—</span>
                </Td>
              </Tr>
            </template>
          </TBody>
        </Table>
      </section>

      <RouteTestDialog v-model:open="testOpen" :route="routeCfg" />
    </template>
  </section>
</template>
