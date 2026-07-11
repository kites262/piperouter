<!-- Routes overview — flat table (PRD §18.3), top routes by request count. -->
<script setup lang="ts">
import { ArrowRight, Waypoints } from 'lucide-vue-next'
import { computed } from 'vue'
import { RouterLink } from 'vue-router'

import type { RouteMetrics } from '@/api/types'
import EmptyState from '@/components/ui/EmptyState.vue'
import Table from '@/components/ui/Table.vue'
import TBody from '@/components/ui/TBody.vue'
import Td from '@/components/ui/Td.vue'
import Th from '@/components/ui/Th.vue'
import THead from '@/components/ui/THead.vue'
import Tr from '@/components/ui/Tr.vue'

import { formatCount, formatErrorRate, formatMs, formatRelativeTime } from './format'

const props = withDefaults(
  defineProps<{
    routes: RouteMetrics[]
    /** Route name → prefix, from the route configuration. */
    prefixes: Record<string, string>
    loading?: boolean
  }>(),
  { loading: false },
)

const MAX_ROWS = 10

const top = computed(() =>
  [...props.routes]
    .sort((a, b) => (b.total !== a.total ? b.total - a.total : a.name.localeCompare(b.name)))
    .slice(0, MAX_ROWS),
)

/**
 * Upstream errors normally surface as 5xx responses and are already counted
 * in status_5xx — max() avoids double counting while still covering upstream
 * errors that never produced a written status.
 */
function routeErrors(r: RouteMetrics): number {
  return Math.max(r.status_5xx, r.upstream_errors)
}
</script>

<template>
  <section class="space-y-3">
    <header class="flex items-center justify-between">
      <h2 class="text-sm font-semibold text-fg">Routes overview</h2>
      <RouterLink
        to="/routes"
        class="inline-flex items-center gap-1 text-xs text-fg-muted transition-colors duration-150 hover:text-accent"
      >
        All routes <ArrowRight class="h-3 w-3" />
      </RouterLink>
    </header>

    <div v-if="loading" class="glass-panel space-y-3 p-4" aria-hidden="true">
      <div v-for="i in 6" :key="i" class="h-4 animate-pulse rounded bg-surface-raised" />
    </div>

    <EmptyState
      v-else-if="top.length === 0"
      title="No routes configured"
      description="Add your first route to start proxying traffic through PipeRouter."
    >
      <template #icon><Waypoints class="h-5 w-5" /></template>
      <RouterLink
        to="/routes"
        class="text-sm font-medium text-accent transition-colors duration-150 hover:text-accent-hover"
      >
        Go to Routes →
      </RouterLink>
    </EmptyState>

    <Table v-else>
      <THead>
        <tr>
          <Th>Route</Th>
          <Th>Prefix</Th>
          <Th><span class="block text-right">Requests</span></Th>
          <Th><span class="block text-right">Error rate</span></Th>
          <Th><span class="block text-right">P95</span></Th>
          <Th><span class="block text-right">Last request</span></Th>
        </tr>
      </THead>
      <TBody>
        <Tr v-for="r in top" :key="r.name">
          <Td>
            <RouterLink
              :to="`/routes/${encodeURIComponent(r.name)}`"
              class="font-medium text-fg transition-colors duration-150 hover:text-accent"
            >
              {{ r.name }}
            </RouterLink>
          </Td>
          <Td>
            <span class="font-mono text-xs">{{ prefixes[r.name] ?? '—' }}</span>
          </Td>
          <Td>
            <div class="text-right font-mono">{{ formatCount(r.total) }}</div>
          </Td>
          <Td>
            <div
              class="text-right font-mono"
              :class="routeErrors(r) > 0 ? 'text-danger' : ''"
            >
              {{ formatErrorRate(routeErrors(r), r.total) }}
            </div>
          </Td>
          <Td>
            <div class="text-right font-mono">
              {{ r.latency.count > 0 ? formatMs(r.latency.p95_ms) : '—' }}
            </div>
          </Td>
          <Td>
            <div class="text-right text-xs text-fg-muted">
              {{ formatRelativeTime(r.last_request_at) }}
            </div>
          </Td>
        </Tr>
      </TBody>
    </Table>
  </section>
</template>
