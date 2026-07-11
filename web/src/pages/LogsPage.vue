<!-- Logs page (PRD §19.6): recent access-log ring buffer.
     2s polling with pause/resume, manual refresh, route/status/limit filters.
     Entries arrive newest-first from the API and are never re-sorted.
     No bodies, no headers — ever. -->
<script setup lang="ts">
import { AlertTriangle, ScrollText, SearchX } from 'lucide-vue-next'
import { computed, onMounted, ref, watch } from 'vue'

import { ApiError, getLogs, listRoutes } from '@/api/client'
import type { LogsQuery, LogsResponse, StatusClass } from '@/api/types'
import LogsFilterBar from '@/components/logs/LogsFilterBar.vue'
import LogsTable from '@/components/logs/LogsTable.vue'
import Button from '@/components/ui/Button.vue'
import EmptyState from '@/components/ui/EmptyState.vue'
import { usePolling } from '@/composables/usePolling'
import { useToast } from '@/composables/useToast'

const toast = useToast()

// --- filters ---------------------------------------------------------------
const routeFilter = ref('') // '' = all routes
const statusFilter = ref('') // '' = all status classes
const limitFilter = ref('100') // 50 | 100 | 500

const routeNames = ref<string[]>([])

// --- data ------------------------------------------------------------------
const logs = ref<LogsResponse | null>(null)
const loadError = ref<string | null>(null)

const entries = computed(() => logs.value?.entries ?? [])
const isInitialLoading = computed(() => logs.value === null && loadError.value === null)
const hasActiveFilters = computed(() => routeFilter.value !== '' || statusFilter.value !== '')

// Discard out-of-order responses (filter change racing a poll tick).
let requestSeq = 0

async function fetchLogs(): Promise<void> {
  const seq = ++requestSeq
  const query: LogsQuery = { limit: Number(limitFilter.value) }
  if (routeFilter.value !== '') query.route = routeFilter.value
  if (statusFilter.value !== '') query.status_class = statusFilter.value as StatusClass

  try {
    const res = await getLogs(query)
    if (seq !== requestSeq) return
    logs.value = res
    loadError.value = null
  } catch (err) {
    if (seq !== requestSeq) return
    const message =
      err instanceof ApiError ? (err.detail !== '' ? err.detail : err.code) : String(err)
    // Toast only on the ok → failed transition; polling every 2s must not spam.
    if (loadError.value === null) {
      toast.error('Failed to load logs', { message })
    }
    loadError.value = message
  }
}

// --- polling (2s) ------------------------------------------------------------
const { pause, resume, isPaused } = usePolling(fetchLogs, { interval: 2000 })

function togglePause(): void {
  if (isPaused.value) {
    resume()
  } else {
    pause()
  }
}

// Filter changes refetch immediately, even while paused.
watch([routeFilter, statusFilter, limitFilter], () => {
  void fetchLogs()
})

// --- manual refresh ----------------------------------------------------------
const refreshing = ref(false)

async function onManualRefresh(): Promise<void> {
  refreshing.value = true
  try {
    await fetchLogs()
  } finally {
    refreshing.value = false
  }
}

function clearFilters(): void {
  routeFilter.value = ''
  statusFilter.value = ''
}

// --- route names for the filter (one-shot, non-fatal on failure) -------------
onMounted(async () => {
  try {
    routeNames.value = (await listRoutes()).map((r) => r.name)
  } catch (err) {
    const message =
      err instanceof ApiError ? (err.detail !== '' ? err.detail : err.code) : String(err)
    toast.warning('Route filter unavailable', { message })
  }
})
</script>

<template>
  <section class="space-y-5">
    <div>
      <h1 class="text-lg font-semibold text-fg">Logs</h1>
      <p class="mt-1 text-sm text-fg-muted">
        Recent access log ring buffer — no bodies, no headers.
      </p>
    </div>

    <LogsFilterBar
      v-model:route="routeFilter"
      v-model:status-class="statusFilter"
      v-model:limit="limitFilter"
      :routes="routeNames"
      :paused="isPaused"
      :refreshing="refreshing"
      @refresh="onManualRefresh"
      @toggle-pause="togglePause"
    />

    <!-- Refresh failed but stale data is still on screen. -->
    <div
      v-if="loadError !== null && logs !== null"
      class="flex items-center gap-2.5 rounded-md border border-danger/30 bg-danger-soft px-3 py-2 text-sm text-danger"
      role="alert"
    >
      <AlertTriangle class="h-4 w-4 shrink-0" />
      <span class="min-w-0 truncate">
        Failed to refresh: <span class="font-mono text-xs">{{ loadError }}</span> — showing last
        loaded entries.
      </span>
    </div>

    <!-- Initial loading skeleton -->
    <div v-if="isInitialLoading" class="card-flat overflow-hidden" aria-busy="true">
      <div class="border-b border-border px-3 py-2.5">
        <div class="h-3 w-2/3 animate-pulse rounded bg-surface-raised" />
      </div>
      <div class="divide-y divide-border">
        <div v-for="i in 8" :key="i" class="flex items-center gap-3 px-3 py-2.5">
          <div class="h-3 w-24 shrink-0 animate-pulse rounded bg-surface-raised" />
          <div class="h-3 w-16 shrink-0 animate-pulse rounded bg-surface-raised" />
          <div class="h-3 flex-1 animate-pulse rounded bg-surface-raised" />
          <div class="h-3 w-14 shrink-0 animate-pulse rounded bg-surface-raised" />
          <div class="h-3 w-10 shrink-0 animate-pulse rounded bg-surface-raised" />
        </div>
      </div>
    </div>

    <!-- Initial load failed, nothing to show -->
    <div
      v-else-if="logs === null"
      class="card-flat flex flex-col items-center gap-3 px-6 py-12 text-center"
    >
      <AlertTriangle class="h-6 w-6 text-danger" />
      <p class="text-sm font-medium text-fg">Failed to load logs</p>
      <p class="max-w-md font-mono text-xs text-danger">{{ loadError }}</p>
      <Button variant="outline" size="sm" :loading="refreshing" @click="onManualRefresh">
        Retry
      </Button>
    </div>

    <template v-else>
      <EmptyState
        v-if="entries.length === 0 && hasActiveFilters"
        title="No entries match the current filters"
        description="Try a different route or status class, or clear the filters."
      >
        <template #icon><SearchX class="h-5 w-5" /></template>
        <Button variant="outline" size="sm" @click="clearFilters">Clear filters</Button>
      </EmptyState>
      <EmptyState
        v-else-if="entries.length === 0"
        title="No requests logged yet"
        description="Entries appear here as soon as traffic flows through the proxy."
      >
        <template #icon><ScrollText class="h-5 w-5" /></template>
      </EmptyState>
      <LogsTable v-else :entries="entries" />

      <p class="font-mono text-xs text-fg-muted">
        showing {{ entries.length }} entries · ring capacity {{ logs.capacity }} · dropped
        {{ logs.dropped }}
      </p>
    </template>
  </section>
</template>
