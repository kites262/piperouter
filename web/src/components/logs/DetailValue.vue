<script setup lang="ts">
import type { LongStringPreview } from '@/api/types'

defineProps<{ value: unknown }>()
defineEmits<{ 'long-string': [ref: string] }>()

function isLongString(value: unknown): value is LongStringPreview {
  return (
    typeof value === 'object' &&
    value !== null &&
    (value as Record<string, unknown>).type === 'long_string' &&
    typeof (value as Record<string, unknown>).value_ref === 'string'
  )
}

function isObject(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null
}

function scalar(value: unknown): string {
  if (value === null) return 'null'
  if (typeof value === 'string') return JSON.stringify(value)
  if (typeof value === 'number' || typeof value === 'boolean') return String(value)
  return JSON.stringify(value) ?? ''
}
</script>

<template>
  <button
    v-if="isLongString(value)"
    type="button"
    class="rounded bg-surface-raised px-1 text-left font-mono text-accent hover:bg-accent/15"
    :title="`${value.length} characters — click to show all`"
    @click="$emit('long-string', value.value_ref)"
  >
    {{ value.prefix }}<span class="mx-1 rounded bg-accent/20 px-1" aria-label="omitted content">…</span>{{ value.suffix }}
  </button>

  <template v-else-if="Array.isArray(value)">
    <span class="text-fg-muted">[</span>
    <div class="ml-3 space-y-1 border-l border-border/60 pl-2">
      <div v-for="(item, index) in value" :key="index">
        <span class="mr-1 text-fg-muted">{{ index }}:</span>
        <DetailValue :value="item" @long-string="$emit('long-string', $event)" />
      </div>
    </div>
    <span class="text-fg-muted">]</span>
  </template>

  <template v-else-if="isObject(value)">
    <div class="space-y-1 border-l border-border/60 pl-2">
      <div v-for="(item, key) in value" :key="key" class="min-w-0">
        <span class="mr-1 text-accent">{{ key }}:</span>
        <DetailValue :value="item" @long-string="$emit('long-string', $event)" />
      </div>
    </div>
  </template>

  <span v-else class="whitespace-pre-wrap break-words">{{ scalar(value) }}</span>
</template>
