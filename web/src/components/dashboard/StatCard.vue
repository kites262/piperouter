<!-- Dashboard stat card — glass surface per PRD §18.3 (dashboard cards only). -->
<script setup lang="ts">
import StatusDot from '@/components/ui/StatusDot.vue'

const props = withDefaults(
  defineProps<{
    label: string
    value: string
    /** Secondary line under the value. */
    sub?: string
    /** Color of the value text. */
    tone?: 'default' | 'success' | 'warning' | 'danger'
    /** Monospace value — numbers, revisions, durations (PRD §18). */
    mono?: boolean
    /** Optional breathing status dot next to the value. */
    dot?: 'success' | 'warning' | 'danger' | 'muted'
  }>(),
  { sub: '', tone: 'default', mono: true, dot: undefined },
)

const tones = {
  default: 'text-fg',
  success: 'text-success',
  warning: 'text-warning',
  danger: 'text-danger',
} as const
</script>

<template>
  <div class="glass rounded-xl p-4">
    <p class="text-[11px] font-medium uppercase tracking-widest text-fg-muted">{{ label }}</p>
    <div class="mt-2 flex min-w-0 items-center gap-2">
      <StatusDot v-if="dot" :status="dot" />
      <span
        class="truncate text-xl font-semibold leading-tight"
        :class="[tones[props.tone], mono ? 'font-mono' : '']"
        :title="value"
      >
        {{ value }}
      </span>
    </div>
    <p v-if="sub" class="mt-1.5 truncate text-xs text-fg-muted" :title="sub">{{ sub }}</p>
  </div>
</template>
