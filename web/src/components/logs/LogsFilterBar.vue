<!-- Logs filter bar (PRD §19.6): route / status class / limit filters,
     manual refresh and a visually obvious pause/resume toggle. -->
<script setup lang="ts">
import { Pause, Play, RotateCw } from 'lucide-vue-next'

import Button from '@/components/ui/Button.vue'
import Select from '@/components/ui/Select.vue'
import StatusDot from '@/components/ui/StatusDot.vue'

/** '' means "all" for route and statusClass. Limit is a stringified number. */
const route = defineModel<string>('route', { required: true })
const statusClass = defineModel<string>('statusClass', { required: true })
const limit = defineModel<string>('limit', { required: true })

defineProps<{
  /** Route names for the route filter (config order). */
  routes: string[]
  /** True while auto-refresh is paused. */
  paused: boolean
  /** True while a manual refresh is in flight. */
  refreshing: boolean
}>()

defineEmits<{ refresh: []; 'toggle-pause': [] }>()
</script>

<template>
  <div class="glass-toolbar flex flex-wrap items-end gap-3 p-3">
    <label class="flex min-w-40 flex-col gap-1">
      <span class="text-xs font-medium text-fg-muted">Route</span>
      <Select v-model="route">
        <option value="">All routes</option>
        <option v-for="name in routes" :key="name" :value="name">{{ name }}</option>
      </Select>
    </label>

    <label class="flex min-w-32 flex-col gap-1">
      <span class="text-xs font-medium text-fg-muted">Status</span>
      <Select v-model="statusClass">
        <option value="">All</option>
        <option value="2xx">2xx</option>
        <option value="3xx">3xx</option>
        <option value="4xx">4xx</option>
        <option value="5xx">5xx</option>
        <option value="error">Error</option>
      </Select>
    </label>

    <label class="flex min-w-24 flex-col gap-1">
      <span class="text-xs font-medium text-fg-muted">Limit</span>
      <Select v-model="limit">
        <option value="50">50</option>
        <option value="100">100</option>
        <option value="500">500</option>
      </Select>
    </label>

    <div class="ml-auto flex flex-wrap items-center gap-2">
      <!-- Live / paused indicator — obvious state change when paused. -->
      <div
        class="flex h-9 items-center gap-2 rounded-md border px-3 text-xs font-medium"
        :class="
          paused
            ? 'border-warning/40 bg-warning-soft text-warning'
            : 'border-border text-fg-secondary'
        "
        role="status"
      >
        <StatusDot :status="paused ? 'warning' : 'success'" :pulse="!paused" />
        {{ paused ? 'Paused' : 'Live · 2s' }}
      </div>

      <Button
        :variant="paused ? 'default' : 'outline'"
        :aria-pressed="paused"
        @click="$emit('toggle-pause')"
      >
        <component :is="paused ? Play : Pause" class="h-4 w-4" />
        {{ paused ? 'Resume' : 'Pause' }}
      </Button>

      <Button variant="outline" :loading="refreshing" @click="$emit('refresh')">
        <RotateCw v-if="!refreshing" class="h-4 w-4" />
        Refresh
      </Button>
    </div>
  </div>
</template>
