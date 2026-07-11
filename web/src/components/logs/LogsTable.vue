<!-- Flat dense monospace access-log table (PRD §19.6).
     Built page-locally (tighter cells than the shared Table kit) with a fixed
     column layout so newly polled rows never cause horizontal jank.
     Never renders bodies or headers. -->
<script setup lang="ts">
import { ArrowLeftRight, Rss } from 'lucide-vue-next'

import type { AccessLogEntry } from '@/api/types'
import Badge from '@/components/ui/Badge.vue'
import Tooltip from '@/components/ui/Tooltip.vue'

defineProps<{ entries: AccessLogEntry[] }>()

type BadgeVariant = 'default' | 'accent' | 'success' | 'warning' | 'danger' | 'muted'

/** Stable row key: entries arrive newest-first and are never re-sorted. */
function rowKey(entry: AccessLogEntry, index: number): string {
  return `${entry.time}|${entry.path}|${index}`
}

/** HH:MM:SS.mmm in the viewer's local time zone. */
function formatTime(iso: string): string {
  const d = new Date(iso)
  if (Number.isNaN(d.getTime())) return iso
  const hh = String(d.getHours()).padStart(2, '0')
  const mm = String(d.getMinutes()).padStart(2, '0')
  const ss = String(d.getSeconds()).padStart(2, '0')
  const ms = String(d.getMilliseconds()).padStart(3, '0')
  return `${hh}:${mm}:${ss}.${ms}`
}

function formatDuration(ms: number): string {
  if (!Number.isFinite(ms) || ms < 0) return '—'
  if (ms < 1) return ms.toFixed(2)
  if (ms < 10) return ms.toFixed(1)
  return String(Math.round(ms))
}

function methodVariant(method: string): BadgeVariant {
  switch (method) {
    case 'GET':
    case 'HEAD':
      return 'default'
    case 'DELETE':
      return 'danger'
    case 'POST':
    case 'PUT':
    case 'PATCH':
      return 'accent'
    default:
      return 'muted'
  }
}

function statusVariant(status: number): BadgeVariant {
  if (status >= 200 && status < 300) return 'success'
  if (status >= 300 && status < 400) return 'accent'
  if (status >= 400 && status < 500) return 'warning'
  if (status >= 500) return 'danger'
  return 'muted'
}
</script>

<template>
  <div class="card-flat overflow-x-auto">
    <table class="w-full min-w-[64rem] table-fixed border-collapse font-mono text-xs">
      <colgroup>
        <col class="w-28" />
        <col class="w-32" />
        <col class="w-20" />
        <col />
        <col class="w-18" />
        <col class="w-24" />
        <col class="w-28" />
        <col class="w-16" />
        <col class="w-44" />
      </colgroup>
      <thead class="border-b border-border">
        <tr>
          <th class="px-3 py-2 text-left text-[11px] font-medium uppercase tracking-wider text-fg-muted">Time</th>
          <th class="px-3 py-2 text-left text-[11px] font-medium uppercase tracking-wider text-fg-muted">Route</th>
          <th class="px-3 py-2 text-left text-[11px] font-medium uppercase tracking-wider text-fg-muted">Method</th>
          <th class="px-3 py-2 text-left text-[11px] font-medium uppercase tracking-wider text-fg-muted">Path</th>
          <th class="px-3 py-2 text-left text-[11px] font-medium uppercase tracking-wider text-fg-muted">Status</th>
          <th class="px-3 py-2 text-right text-[11px] font-medium uppercase tracking-wider text-fg-muted">
            Duration (ms)
          </th>
          <th class="px-3 py-2 text-left text-[11px] font-medium uppercase tracking-wider text-fg-muted">Transport</th>
          <th class="px-3 py-2 text-left text-[11px] font-medium uppercase tracking-wider text-fg-muted">Stream</th>
          <th class="px-3 py-2 text-left text-[11px] font-medium uppercase tracking-wider text-fg-muted">Error</th>
        </tr>
      </thead>
      <tbody class="divide-y divide-border">
        <tr
          v-for="(entry, index) in entries"
          :key="rowKey(entry, index)"
          class="transition-colors duration-150 hover:bg-surface-raised/50"
        >
          <td class="whitespace-nowrap px-3 py-1.5 text-fg-secondary">{{ formatTime(entry.time) }}</td>
          <td class="truncate px-3 py-1.5" :title="entry.route !== '' ? entry.route : undefined">
            <span v-if="entry.route !== ''" class="text-fg-secondary">{{ entry.route }}</span>
            <span v-else class="text-fg-muted">—</span>
          </td>
          <td class="px-3 py-1.5">
            <Badge :variant="methodVariant(entry.method)" mono>{{ entry.method }}</Badge>
          </td>
          <td class="truncate px-3 py-1.5 text-fg" :title="entry.path">{{ entry.path }}</td>
          <td class="px-3 py-1.5">
            <Badge :variant="statusVariant(entry.status)" mono>
              {{ entry.status > 0 ? entry.status : '—' }}
            </Badge>
          </td>
          <td class="whitespace-nowrap px-3 py-1.5 text-right tabular-nums text-fg-secondary">
            {{ formatDuration(entry.duration_ms) }}
          </td>
          <td class="truncate px-3 py-1.5 text-fg-secondary" :title="entry.transport !== '' ? entry.transport : undefined">
            <template v-if="entry.transport !== ''">{{ entry.transport }}</template>
            <span v-else class="text-fg-muted">—</span>
          </td>
          <td class="px-3 py-1.5">
            <span v-if="entry.streaming === 'sse'" class="inline-flex text-accent" title="SSE">
              <Rss class="h-3.5 w-3.5" />
              <span class="sr-only">SSE</span>
            </span>
            <span v-else-if="entry.streaming === 'websocket'" class="inline-flex text-accent" title="WebSocket">
              <ArrowLeftRight class="h-3.5 w-3.5" />
              <span class="sr-only">WebSocket</span>
            </span>
            <span v-else class="text-fg-muted">—</span>
          </td>
          <td class="overflow-hidden px-3 py-1.5">
            <Tooltip v-if="entry.error !== ''" :text="entry.error" class="w-full max-w-full">
              <span class="block w-full truncate text-danger">{{ entry.error }}</span>
            </Tooltip>
            <span v-else class="text-fg-muted">—</span>
          </td>
        </tr>
      </tbody>
    </table>
  </div>
</template>
