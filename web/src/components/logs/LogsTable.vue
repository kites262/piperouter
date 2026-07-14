<!-- Flat dense monospace access-log table (PRD §19.6).
     Built page-locally (tighter cells than the shared Table kit) with
     table-fixed columns so newly polled rows never cause horizontal jank.
     No artificial min-width — horizontal scroll only when content overflows.
     Main columns: Time / IP / Route / Status / Duration / Path.
     Method, Transport, Stream, Error, and forward headers live in a
     collapsible detail row so Path can use the remaining width on the right.
     IP column is derived from X-Forwarded-For when present. -->
<script setup lang="ts">
import { ArrowLeftRight, ChevronRight, FileText, Waves } from 'lucide-vue-next'
import { ref } from 'vue'

import type { AccessLogEntry } from '@/api/types'
import Badge from '@/components/ui/Badge.vue'

defineProps<{ entries: AccessLogEntry[] }>()

type BadgeVariant = 'default' | 'accent' | 'success' | 'warning' | 'danger' | 'muted'

/** Stable row key: entries arrive newest-first and are never re-sorted. */
function rowKey(entry: AccessLogEntry, index: number): string {
  return `${entry.time}|${entry.path}|${index}`
}

/**
 * Expansion is keyed by entry CONTENT (not index) so an expanded row stays
 * expanded while polling prepends new rows above it.
 */
function contentKey(entry: AccessLogEntry): string {
  return `${entry.time}|${entry.path}|${entry.status}|${entry.duration_ms}`
}

const expanded = ref(new Set<string>())

function toggleExpand(entry: AccessLogEntry): void {
  const key = contentKey(entry)
  const next = new Set(expanded.value)
  if (next.has(key)) {
    next.delete(key)
  } else {
    next.add(key)
  }
  expanded.value = next
}

function isExpanded(entry: AccessLogEntry): boolean {
  return expanded.value.has(contentKey(entry))
}

function hasForwardHeaders(entry: AccessLogEntry): boolean {
  return (entry.forward_headers?.length ?? 0) > 0
}

/**
 * Client IP from X-Forwarded-For only (leftmost address in the chain).
 * Empty when the client did not send that header.
 */
function clientIp(entry: AccessLogEntry): string {
  const xff = entry.forward_headers?.find(
    (h) => h.name.toLowerCase() === 'x-forwarded-for',
  )
  if (!xff?.value) return ''
  // "client, proxy1, proxy2" — original client is leftmost.
  const first = xff.value.split(',')[0]?.trim() ?? ''
  return first
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

function onRowKeydown(event: KeyboardEvent, entry: AccessLogEntry): void {
  if (event.key === 'Enter' || event.key === ' ') {
    event.preventDefault()
    toggleExpand(entry)
  }
}

</script>

<template>
  <!-- overflow-x-auto only kicks in when the table genuinely exceeds the
       card (no artificial min-width). table-fixed + truncate cells keep
       columns stable as rows poll in. Path is the flexible last column. -->
  <div class="card-flat overflow-x-auto">
    <table class="w-full table-fixed border-collapse font-mono text-xs">
      <colgroup>
        <col class="w-8" />
        <col class="w-28" />
        <col class="w-36" />
        <col class="w-32" />
        <col class="w-14" />
        <col class="w-20" />
        <col />
      </colgroup>
      <thead class="border-b border-border">
        <tr>
          <th class="px-1 py-2"><span class="sr-only">Details</span></th>
          <th class="px-3 py-2 text-center text-[11px] font-medium uppercase tracking-wider text-fg-muted">Time</th>
          <th class="px-3 py-2 text-center text-[11px] font-medium uppercase tracking-wider text-fg-muted">IP</th>
          <th class="px-3 py-2 text-center text-[11px] font-medium uppercase tracking-wider text-fg-muted">Route</th>
          <th class="px-2 py-2 text-center text-[11px] font-medium uppercase tracking-wider text-fg-muted">Status</th>
          <th class="px-2 py-2 text-center text-[11px] font-medium uppercase tracking-wider text-fg-muted" title="milliseconds">
            Duration
          </th>
          <th class="px-3 py-2 text-center text-[11px] font-medium uppercase tracking-wider text-fg-muted">Path</th>
        </tr>
      </thead>
      <tbody class="divide-y divide-border">
        <template v-for="(entry, index) in entries" :key="rowKey(entry, index)">
          <tr
            class="cursor-pointer transition-colors duration-150 hover:bg-surface-raised/50 focus-visible:bg-surface-raised/50 focus-visible:outline-2 focus-visible:outline-offset-[-2px] focus-visible:outline-accent"
            tabindex="0"
            role="button"
            :aria-expanded="isExpanded(entry)"
            :title="isExpanded(entry) ? 'Hide details' : 'Show details'"
            @click="toggleExpand(entry)"
            @keydown="onRowKeydown($event, entry)"
          >
            <td class="px-1 py-1.5 text-center">
              <ChevronRight
                class="mx-auto h-3.5 w-3.5 text-fg-muted transition-transform duration-150"
                :class="isExpanded(entry) ? 'rotate-90' : ''"
                aria-hidden="true"
              />
            </td>
            <td class="whitespace-nowrap px-3 py-1.5 text-center text-fg-secondary">{{ formatTime(entry.time) }}</td>
            <td class="truncate px-3 py-1.5 text-center" :title="clientIp(entry) || undefined">
              <span v-if="clientIp(entry)" class="text-fg-secondary">{{ clientIp(entry) }}</span>
              <span v-else class="text-fg-muted">—</span>
            </td>
            <td class="truncate px-3 py-1.5 text-center" :title="entry.route !== '' ? entry.route : undefined">
              <span v-if="entry.route !== ''" class="text-fg-secondary">{{ entry.route }}</span>
              <span v-else class="text-fg-muted">—</span>
            </td>
            <td class="px-2 py-1.5 text-center">
              <Badge :variant="statusVariant(entry.status)" mono>
                {{ entry.status > 0 ? entry.status : '—' }}
              </Badge>
            </td>
            <td class="whitespace-nowrap px-2 py-1.5 text-center tabular-nums text-fg-secondary">
              {{ formatDuration(entry.duration_ms) }}
            </td>
            <td class="truncate border-l border-dashed border-border px-3 py-1.5 text-fg" :title="entry.path">{{ entry.path }}</td>
          </tr>
          <!-- Collapsible detail: Method / Transport / Stream / Error + forward headers. -->
          <tr v-if="isExpanded(entry)" class="bg-bg-deep/40">
            <td class="px-1 py-0" />
            <td :colspan="6" class="px-3 pb-2.5 pt-1.5">
              <dl class="space-y-1">
                <div class="flex items-center gap-2">
                  <dt class="shrink-0 text-[11px] text-fg-muted">Method</dt>
                  <dd>
                    <Badge :variant="methodVariant(entry.method)" mono>{{ entry.method }}</Badge>
                  </dd>
                </div>
                <div class="flex items-baseline gap-2 min-w-0">
                  <dt class="shrink-0 text-[11px] text-fg-muted">Transport</dt>
                  <dd class="min-w-0 truncate text-fg-secondary" :title="entry.transport || undefined">
                    <template v-if="entry.transport !== ''">{{ entry.transport }}</template>
                    <span v-else class="text-fg-muted">—</span>
                  </dd>
                </div>
                <div class="flex items-center gap-2">
                  <dt class="shrink-0 text-[11px] text-fg-muted">Stream</dt>
                  <!-- SSE = continuous waves; WS = duplex; unary = buffered document. -->
                  <dd class="flex items-center gap-1.5">
                    <template v-if="entry.streaming === 'sse'">
                      <Waves class="h-3.5 w-3.5 text-accent" aria-hidden="true" />
                      <span class="text-fg-secondary">SSE</span>
                    </template>
                    <template v-else-if="entry.streaming === 'websocket'">
                      <ArrowLeftRight class="h-3.5 w-3.5 text-accent" aria-hidden="true" />
                      <span class="text-fg-secondary">WebSocket</span>
                    </template>
                    <template v-else>
                      <FileText class="h-3.5 w-3.5 text-fg-muted" aria-hidden="true" />
                      <span class="text-fg-muted">Buffered</span>
                    </template>
                  </dd>
                </div>
                <div class="flex items-start gap-2 min-w-0">
                  <dt class="shrink-0 text-[11px] text-fg-muted">Error</dt>
                  <dd class="min-w-0 break-all">
                    <span v-if="entry.error !== ''" class="text-danger">{{ entry.error }}</span>
                    <span v-else class="text-fg-muted">—</span>
                  </dd>
                </div>
              </dl>
              <dl v-if="hasForwardHeaders(entry)" class="mt-2 space-y-0.5 border-t border-border/60 pt-2">
                <div
                  v-for="h in entry.forward_headers"
                  :key="h.name"
                  class="flex items-baseline gap-2"
                >
                  <dt class="shrink-0 text-[11px] text-fg-muted">{{ h.name }}:</dt>
                  <dd class="min-w-0 break-all text-fg-secondary">{{ h.value }}</dd>
                </div>
              </dl>
            </td>
          </tr>
        </template>
      </tbody>
    </table>
  </div>
</template>
