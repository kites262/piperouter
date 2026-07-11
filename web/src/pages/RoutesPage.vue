<!-- Routes list (PRD §19.2) — table with live per-route metrics + CRUD. -->
<script setup lang="ts">
import { Pencil, Plus, RefreshCw, Route as RouteIcon, Trash2 } from 'lucide-vue-next'
import { computed, onMounted, reactive, ref } from 'vue'
import { RouterLink, useRouter } from 'vue-router'

import {
  ApiError,
  deleteRoute,
  getConfig,
  getMetrics,
  listRoutes,
  listTransports,
  updateRoute,
} from '@/api/client'
import type { RouteConfig, RouteMetrics, TransportConfig } from '@/api/types'
import RouteFormDialog from '@/components/routes/RouteFormDialog.vue'
import Badge from '@/components/ui/Badge.vue'
import Button from '@/components/ui/Button.vue'
import ConfirmDialog from '@/components/ui/ConfirmDialog.vue'
import EmptyState from '@/components/ui/EmptyState.vue'
import Switch from '@/components/ui/Switch.vue'
import Table from '@/components/ui/Table.vue'
import TBody from '@/components/ui/TBody.vue'
import Td from '@/components/ui/Td.vue'
import Th from '@/components/ui/Th.vue'
import THead from '@/components/ui/THead.vue'
import Tooltip from '@/components/ui/Tooltip.vue'
import Tr from '@/components/ui/Tr.vue'
import { usePolling } from '@/composables/usePolling'
import { useToast } from '@/composables/useToast'

const router = useRouter()
const toast = useToast()

// --- data -----------------------------------------------------------------

const routes = ref<RouteConfig[] | null>(null)
const transports = ref<TransportConfig[]>([])
const revision = ref('')
const loadError = ref('')
const metricsByName = ref<Map<string, RouteMetrics>>(new Map())
const metricsError = ref(false)
const pendingToggles = reactive(new Set<string>())

function errMessage(err: unknown): string {
  return err instanceof ApiError ? (err.detail !== '' ? err.detail : err.code) : String(err)
}

async function loadAll(): Promise<void> {
  try {
    const [routeList, transportList, envelope] = await Promise.all([
      listRoutes(),
      listTransports(),
      getConfig(),
    ])
    routes.value = routeList
    transports.value = transportList
    revision.value = envelope.revision
    loadError.value = ''
  } catch (err) {
    const message = errMessage(err)
    if (routes.value === null) {
      loadError.value = message
    } else {
      toast.error('Failed to refresh routes', { message })
    }
  }
}

function retry(): void {
  loadError.value = ''
  void loadAll()
}

onMounted(() => {
  void loadAll()
})

// Per-route metrics poll every 3s (joined into the table by route name).
usePolling(
  async () => {
    try {
      const snapshot = await getMetrics()
      metricsByName.value = new Map(snapshot.routes.map((m) => [m.name, m]))
      metricsError.value = false
    } catch {
      metricsError.value = true
    }
  },
  { interval: 3000 },
)

// --- table rows -----------------------------------------------------------

type BadgeVariant = 'default' | 'accent' | 'success' | 'warning' | 'danger' | 'muted'

interface RouteRow {
  route: RouteConfig
  transportVariant: BadgeVariant
  transportMissing: boolean
  requests: string
  errorRateText: string
  errorRateClass: string
  p95: string
  lastRel: string
  lastAbs: string
}

const numberFormat = new Intl.NumberFormat()

function errorRate(m: RouteMetrics | null): number | null {
  if (m === null || m.total === 0) return null
  // The backend counts one upstream failure in BOTH status_5xx and
  // upstream_errors, so summing them double-counts (and can exceed 100%).
  // Use the larger of the two, matching the Dashboard and Route Detail views.
  return (Math.max(m.status_5xx, m.upstream_errors) / m.total) * 100
}

function formatErrorRate(rate: number | null): string {
  if (rate === null) return '—'
  if (rate === 0) return '0%'
  if (rate < 0.1) return '<0.1%'
  return `${rate.toFixed(rate < 10 ? 1 : 0)}%`
}

function rateClass(rate: number | null): string {
  if (rate === null) return 'text-fg-muted'
  if (rate >= 5) return 'text-danger'
  if (rate >= 1) return 'text-warning'
  return 'text-fg-secondary'
}

function formatP95(m: RouteMetrics | null): string {
  if (m === null || m.latency.count === 0) return '—'
  const ms = m.latency.p95_ms
  if (ms >= 1000) return `${(ms / 1000).toFixed(2)} s`
  if (ms < 1) return '<1 ms'
  return `${Math.round(ms)} ms`
}

function relativeTime(iso: string | null): string {
  if (iso === null) return '—'
  const t = Date.parse(iso)
  if (Number.isNaN(t)) return '—'
  const diff = Math.max(0, Date.now() - t) / 1000
  if (diff < 5) return 'just now'
  if (diff < 60) return `${Math.floor(diff)}s ago`
  if (diff < 3600) return `${Math.floor(diff / 60)}m ago`
  if (diff < 86400) return `${Math.floor(diff / 3600)}h ago`
  return `${Math.floor(diff / 86400)}d ago`
}

const transportTypes = computed(() => new Map(transports.value.map((t) => [t.name, t.type])))

const rows = computed<RouteRow[]>(() => {
  const metrics = metricsByName.value
  const types = transportTypes.value
  return (routes.value ?? []).map((route) => {
    const m = metrics.get(route.name) ?? null
    const type = types.get(route.transport)
    const rate = errorRate(m)
    const last = m?.last_request_at ?? null
    return {
      route,
      transportVariant:
        type === undefined
          ? 'danger'
          : type === 'direct'
            ? 'muted'
            : type === 'http'
              ? 'accent'
              : 'default',
      transportMissing: type === undefined,
      requests: m === null ? '—' : numberFormat.format(m.total),
      errorRateText: formatErrorRate(rate),
      errorRateClass: rateClass(rate),
      p95: formatP95(m),
      lastRel: relativeTime(last),
      lastAbs: last !== null ? new Date(last).toLocaleString() : '',
    }
  })
})

// --- mutations ------------------------------------------------------------

function handleMutationError(err: unknown, title: string): void {
  if (err instanceof ApiError && err.code === 'revision_conflict') {
    toast.warning('Configuration changed', {
      message: 'The config was modified elsewhere — reloading the current state.',
    })
    void loadAll()
    return
  }
  toast.error(title, { message: errMessage(err) })
}

/** Instant enable/disable — optimistic flip with rollback + toast on error. */
async function toggleEnabled(route: RouteConfig, next: boolean): Promise<void> {
  if (pendingToggles.has(route.name)) return
  const payload: RouteConfig = { ...route, enabled: next }
  route.enabled = next
  pendingToggles.add(route.name)
  try {
    await updateRoute(route.name, payload, revision.value)
    await loadAll()
  } catch (err) {
    route.enabled = !next
    handleMutationError(err, `Failed to ${next ? 'enable' : 'disable'} "${route.name}"`)
  } finally {
    pendingToggles.delete(route.name)
  }
}

// --- create / edit dialog ---------------------------------------------------

const formOpen = ref(false)
const formMode = ref<'create' | 'edit'>('create')
const editingRoute = ref<RouteConfig | null>(null)

function openCreate(): void {
  formMode.value = 'create'
  editingRoute.value = null
  formOpen.value = true
}

function openEdit(route: RouteConfig): void {
  formMode.value = 'edit'
  editingRoute.value = { ...route }
  formOpen.value = true
}

// --- delete -----------------------------------------------------------------

const deleteOpen = ref(false)
const deleteTarget = ref<RouteConfig | null>(null)
const deleting = ref(false)

function requestDelete(route: RouteConfig): void {
  deleteTarget.value = route
  deleteOpen.value = true
}

async function confirmDelete(): Promise<void> {
  const target = deleteTarget.value
  if (target === null || deleting.value) return
  deleting.value = true
  try {
    await deleteRoute(target.name, revision.value)
    toast.success(`Route "${target.name}" deleted`)
    deleteOpen.value = false
    await loadAll()
  } catch (err) {
    deleteOpen.value = false
    handleMutationError(err, `Failed to delete "${target.name}"`)
  } finally {
    deleting.value = false
  }
}

// --- navigation -------------------------------------------------------------

function goDetail(name: string): void {
  void router.push({ name: 'route-detail', params: { name } })
}
</script>

<template>
  <section class="space-y-6">
    <div class="flex items-start justify-between gap-4 animate-fade-up">
      <div>
        <h1 class="text-lg font-semibold tracking-tight text-fg">Routes</h1>
        <p class="mt-1 text-sm text-fg-muted">
          Prefix → target mappings with live per-route metrics.
        </p>
      </div>
      <Button @click="openCreate">
        <Plus class="h-4 w-4" />
        New Route
      </Button>
    </div>

    <div v-if="routes === null && loadError === ''" class="glass-panel p-4" aria-busy="true">
      <div class="animate-pulse space-y-3">
        <div class="h-4 w-1/3 rounded bg-surface-raised" />
        <div v-for="i in 5" :key="i" class="h-9 rounded bg-surface-raised/70" />
      </div>
    </div>

    <div
      v-else-if="routes === null"
      class="glass-panel flex flex-col items-center gap-3 px-6 py-12 text-center"
    >
      <p class="text-sm font-medium text-danger">Failed to load routes</p>
      <p class="max-w-md break-all font-mono text-xs text-fg-muted">{{ loadError }}</p>
      <Button variant="outline" size="sm" @click="retry">
        <RefreshCw class="h-3.5 w-3.5" />
        Retry
      </Button>
    </div>

    <!-- Empty state -->
    <EmptyState
      v-else-if="routes.length === 0"
      title="No routes configured"
      description="Create your first route to start forwarding requests through PipeRouter."
    >
      <template #icon><RouteIcon class="h-5 w-5" /></template>
      <Button size="sm" @click="openCreate">
        <Plus class="h-4 w-4" />
        New Route
      </Button>
    </EmptyState>

    <div v-else class="animate-fade-up stagger-2 space-y-2">
      <p v-if="metricsError" class="text-xs text-warning">
        Metrics unavailable — retrying every 3s. Route data may be stale.
      </p>
      <Table>
        <colgroup>
          <col class="w-[12%]" />
          <col class="w-[4.5rem]" />
          <col class="w-[12%]" />
          <col />
          <col class="w-[10%]" />
          <col class="w-[7%]" />
          <col class="w-[7%]" />
          <col class="w-[7%]" />
          <col class="w-[9%]" />
          <!-- 2× size-sm icon buttons + cell padding — same right inset as Transports. -->
          <col class="w-[6.5rem]" />
        </colgroup>
        <THead>
          <tr>
            <Th>Name</Th>
            <Th>Enabled</Th>
            <Th>Prefix</Th>
            <Th>Target</Th>
            <Th>Transport</Th>
            <Th><span class="block text-right">Requests</span></Th>
            <Th><span class="block text-right">Error rate</span></Th>
            <Th><span class="block text-right">P95</span></Th>
            <Th>Last request</Th>
            <Th><span class="sr-only">Actions</span></Th>
          </tr>
        </THead>
        <TBody>
          <Tr
            v-for="row in rows"
            :key="row.route.name"
            class="cursor-pointer"
            @click="goDetail(row.route.name)"
          >
            <Td>
              <RouterLink
                :to="{ name: 'route-detail', params: { name: row.route.name } }"
                class="block truncate font-medium text-fg transition-colors duration-150 hover:text-accent"
                @click.stop
              >
                {{ row.route.name }}
              </RouterLink>
            </Td>
            <Td @click.stop>
              <Switch
                :model-value="row.route.enabled"
                :disabled="pendingToggles.has(row.route.name)"
                :aria-label="`Toggle route ${row.route.name}`"
                @update:model-value="(value: boolean) => void toggleEnabled(row.route, value)"
              />
            </Td>
            <Td>
              <span class="block truncate font-mono text-xs text-fg">{{ row.route.prefix }}</span>
            </Td>
            <Td>
              <Tooltip :text="row.route.target">
                <span class="block truncate font-mono text-xs text-fg-secondary">
                  {{ row.route.target }}
                </span>
              </Tooltip>
            </Td>
            <Td>
              <Tooltip v-if="row.transportMissing" text="Transport not found in config">
                <Badge variant="danger" mono>{{ row.route.transport }}</Badge>
              </Tooltip>
              <Badge v-else :variant="row.transportVariant" mono>
                {{ row.route.transport }}
              </Badge>
            </Td>
            <Td>
              <span class="block truncate text-right font-mono text-xs tabular-nums text-fg-secondary">
                {{ row.requests }}
              </span>
            </Td>
            <Td>
              <span
                class="block truncate text-right font-mono text-xs tabular-nums"
                :class="row.errorRateClass"
              >
                {{ row.errorRateText }}
              </span>
            </Td>
            <Td>
              <span class="block truncate text-right font-mono text-xs tabular-nums text-fg-secondary">
                {{ row.p95 }}
              </span>
            </Td>
            <Td>
              <Tooltip v-if="row.lastAbs" :text="row.lastAbs">
                <span class="block truncate text-xs text-fg-secondary">{{ row.lastRel }}</span>
              </Tooltip>
              <span v-else class="text-xs text-fg-muted">—</span>
            </Td>
            <Td @click.stop>
              <div class="flex items-center justify-end gap-1">
                <Button
                  variant="ghost"
                  size="sm"
                  :aria-label="`Edit route ${row.route.name}`"
                  @click="openEdit(row.route)"
                >
                  <Pencil class="h-3.5 w-3.5" />
                </Button>
                <Button
                  variant="ghost"
                  size="sm"
                  class="hover:text-danger"
                  :aria-label="`Delete route ${row.route.name}`"
                  @click="requestDelete(row.route)"
                >
                  <Trash2 class="h-3.5 w-3.5" />
                </Button>
              </div>
            </Td>
          </Tr>
        </TBody>
      </Table>
    </div>

    <RouteFormDialog
      v-model:open="formOpen"
      :mode="formMode"
      :route="editingRoute"
      :routes="routes ?? []"
      :transports="transports"
      :revision="revision"
      @saved="() => void loadAll()"
      @reload="() => void loadAll()"
    />

    <ConfirmDialog
      v-model:open="deleteOpen"
      title="Delete route"
      :message="
        deleteTarget
          ? `Delete route “${deleteTarget.name}” (${deleteTarget.prefix} → ${deleteTarget.target})? This cannot be undone.`
          : ''
      "
      confirm-text="Delete"
      danger
      :loading="deleting"
      @confirm="confirmDelete"
    />
  </section>
</template>
