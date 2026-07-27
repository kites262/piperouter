<!-- Logs page (PRD §19.6): filterable, auto-refreshing access-log ring buffer.
     Pause freezes the 2s poll so the operator can inspect a row without it
     scrolling away; Clear empties the in-memory buffer after confirmation. -->
<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref, watch } from 'vue'
import { AlertTriangle, ScrollText, SearchX } from 'lucide-vue-next'

import { ApiError, clearLogs, getLogs, listRoutes } from '@/api/client'
import type { LogsResponse, StatusClass } from '@/api/types'
import LogsFilterBar from '@/components/logs/LogsFilterBar.vue'
import LogsTable from '@/components/logs/LogsTable.vue'
import Button from '@/components/ui/Button.vue'
import ConfirmDialog from '@/components/ui/ConfirmDialog.vue'
import EmptyState from '@/components/ui/EmptyState.vue'
import { useToast } from '@/composables/useToast'

const POLL_MS = 2000

const toast = useToast()

const logs = ref<LogsResponse | null>(null)
const routeNames = ref<string[]>([])
const loadError = ref<string | null>(null)
const refreshing = ref(false)
const isPaused = ref(false)

const routeFilter = ref('')
const statusFilter = ref('')
const limitFilter = ref('100')

const clearOpen = ref(false)
const clearing = ref(false)

let timer: ReturnType<typeof setInterval> | null = null

const entries = computed(() => logs.value?.entries ?? [])
const isInitialLoading = computed(() => logs.value === null && loadError.value === null)
const hasActiveFilters = computed(
  () => routeFilter.value !== '' || statusFilter.value !== '' || limitFilter.value !== '100',
)

async function fetchLogs(opts: { silent?: boolean } = {}): Promise<void> {
  if (!opts.silent) refreshing.value = true
  try {
    const limit = Number.parseInt(limitFilter.value, 10)
    logs.value = await getLogs({
      limit: Number.isFinite(limit) ? limit : 100,
      route: routeFilter.value || undefined,
      status_class: (statusFilter.value || undefined) as StatusClass | undefined,
    })
    loadError.value = null
  } catch (err) {
    const msg = err instanceof ApiError ? err.message : 'Failed to load logs'
    loadError.value = msg
    // Keep the previous table on poll failures; only toast on manual refresh.
    if (!opts.silent) toast.error(msg)
  } finally {
    refreshing.value = false
  }
}

function startPolling(): void {
  stopPolling()
  if (isPaused.value) return
  timer = setInterval(() => {
    void fetchLogs({ silent: true })
  }, POLL_MS)
}

function stopPolling(): void {
  if (timer !== null) {
    clearInterval(timer)
    timer = null
  }
}

function togglePause(): void {
  isPaused.value = !isPaused.value
  if (isPaused.value) stopPolling()
  else startPolling()
}

async function onManualRefresh(): Promise<void> {
  await fetchLogs()
}

function clearFilters(): void {
  routeFilter.value = ''
  statusFilter.value = ''
  limitFilter.value = '100'
}

function openClearDialog(): void {
  clearOpen.value = true
}

async function confirmClearLogs(): Promise<void> {
  clearing.value = true
  try {
    await clearLogs()
    clearOpen.value = false
    toast.success('Access log buffer cleared')
    await fetchLogs({ silent: true })
  } catch (err) {
    toast.error(err instanceof ApiError ? err.message : 'Failed to clear logs')
  } finally {
    clearing.value = false
  }
}

// Filters change → immediate reload (even while paused; the operator asked).
watch([routeFilter, statusFilter, limitFilter], () => {
  void fetchLogs({ silent: true })
})

onMounted(async () => {
  try {
    const routes = await listRoutes()
    routeNames.value = routes.map((r) => r.name)
  } catch {
    // Route list is only for the filter dropdown; ignore failures.
  }
  await fetchLogs()
  startPolling()
})

onUnmounted(stopPolling)
</script>

<template>
  <section class="space-y-5">
    <div class="animate-fade-up">
      <h1 class="text-lg font-semibold tracking-tight text-fg">Logs</h1>
      <p class="mt-1 text-sm text-fg-muted">
        Recent access log ring buffer — no bodies, no credentials. Rows with forward headers
        (X-Forwarded-*) expand to show them.
      </p>
    </div>

    <div class="animate-fade-up stagger-1">
      <LogsFilterBar
        v-model:route="routeFilter"
        v-model:status-class="statusFilter"
        v-model:limit="limitFilter"
        :routes="routeNames"
        :paused="isPaused"
        :refreshing="refreshing"
        :clearing="clearing"
        @refresh="onManualRefresh"
        @toggle-pause="togglePause"
        @clear="openClearDialog"
      />
    </div>

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

    <ConfirmDialog
      v-model:open="clearOpen"
      title="Clear access logs"
      message="Discard every entry in the in-memory access-log ring buffer? Capacity and the dropped counter are kept. This cannot be undone."
      confirm-text="Clear logs"
      danger
      :loading="clearing"
      @confirm="confirmClearLogs"
    />
  </section>
</template>
