<!-- Recent errors — flat dense list from the recent-logs ring buffer (PRD §19.1). -->
<script setup lang="ts">
import { ArrowRight, ShieldCheck } from 'lucide-vue-next'
import { computed } from 'vue'
import { RouterLink } from 'vue-router'

import type { AccessLogEntry } from '@/api/types'
import Badge from '@/components/ui/Badge.vue'
import EmptyState from '@/components/ui/EmptyState.vue'

import { formatLocalDateTime, formatLocalTime } from './format'

const props = withDefaults(
  defineProps<{
    entries: AccessLogEntry[]
    loading?: boolean
  }>(),
  { loading: false },
)

const MAX_ROWS = 20

const rows = computed(() => props.entries.slice(0, MAX_ROWS))

function statusVariant(e: AccessLogEntry): 'danger' | 'warning' | 'muted' {
  if (e.status >= 500) return 'danger'
  if (e.status >= 400) return 'warning'
  return 'muted'
}

function statusLabel(e: AccessLogEntry): string {
  return e.status > 0 ? String(e.status) : 'ERR'
}
</script>

<template>
  <section class="space-y-3">
    <header class="flex items-center justify-between">
      <h2 class="text-sm font-semibold text-fg">Recent errors</h2>
      <RouterLink
        to="/logs"
        class="inline-flex items-center gap-1 text-xs text-fg-muted transition-colors duration-150 hover:text-accent"
      >
        Open logs <ArrowRight class="h-3 w-3" />
      </RouterLink>
    </header>

    <div v-if="loading" class="card-flat divide-y divide-border" aria-hidden="true">
      <div v-for="i in 4" :key="i" class="px-3 py-3">
        <div class="h-3.5 w-3/4 animate-pulse rounded bg-surface-raised" />
        <div class="mt-2 h-3 w-1/2 animate-pulse rounded bg-surface-raised" />
      </div>
    </div>

    <EmptyState
      v-else-if="rows.length === 0"
      title="No recent errors"
      description="No error or 5xx entries in the recent logs buffer."
    >
      <template #icon><ShieldCheck class="h-5 w-5" /></template>
    </EmptyState>

    <ul v-else class="card-flat divide-y divide-border overflow-hidden">
      <li
        v-for="(e, i) in rows"
        :key="`${e.time}-${i}`"
        class="flex items-start gap-2.5 px-3 py-2.5"
        :title="formatLocalDateTime(e.time)"
      >
        <Badge :variant="statusVariant(e)" mono>{{ statusLabel(e) }}</Badge>
        <div class="min-w-0 flex-1">
          <p class="truncate font-mono text-xs text-fg" :title="`${e.method} ${e.path}`">
            <span class="text-fg-secondary">{{ e.method }}</span>
            {{ e.path }}
          </p>
          <p class="mt-0.5 flex min-w-0 items-center gap-1.5 text-[11px] text-fg-muted">
            <span class="shrink-0 font-mono">{{ formatLocalTime(e.time) }}</span>
            <span aria-hidden="true">·</span>
            <span class="truncate">{{ e.route !== '' ? e.route : 'unmatched' }}</span>
            <template v-if="e.error !== ''">
              <span aria-hidden="true">·</span>
              <span class="truncate font-mono text-danger">{{ e.error }}</span>
            </template>
          </p>
        </div>
      </li>
    </ul>
  </section>
</template>
