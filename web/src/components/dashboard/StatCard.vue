<!-- Dashboard stat card — elevated liquid-glass surface (PRD §18.3). Supports
     an optional tweened numeric value and a breathing status dot. -->
<script setup lang="ts">
import AnimatedNumber from '@/components/ui/AnimatedNumber.vue'
import StatusDot from '@/components/ui/StatusDot.vue'

const props = withDefaults(
  defineProps<{
    label: string
    /** Static display value (used when `animate` is not provided). */
    value: string
    /** Secondary line under the value. */
    sub?: string
    /** Color of the value text. */
    tone?: 'default' | 'success' | 'warning' | 'danger'
    /** Monospace value — numbers, revisions, durations (PRD §18). */
    mono?: boolean
    /** Optional breathing status dot next to the value. */
    dot?: 'success' | 'warning' | 'danger' | 'accent' | 'muted'
    /** When set, the value is tweened to this number instead of shown static. */
    animate?: number
    /** Formatter for the animated number. */
    format?: (n: number) => string
  }>(),
  { sub: '', tone: 'default', mono: true, dot: undefined, animate: undefined, format: undefined },
)

const tones = {
  default: 'text-fg',
  success: 'text-success',
  warning: 'text-warning',
  danger: 'text-danger',
} as const
</script>

<template>
  <div class="glass-panel card-lift p-4">
    <p class="text-[11px] font-medium uppercase tracking-widest text-fg-muted">{{ label }}</p>
    <div class="mt-2 flex min-w-0 items-center gap-2">
      <StatusDot v-if="dot" :status="dot" />
      <span
        class="truncate text-xl font-semibold leading-tight"
        :class="[tones[props.tone], mono ? 'font-mono tnums' : '']"
        :title="value"
      >
        <AnimatedNumber v-if="animate !== undefined" :value="animate" :format="format" />
        <template v-else>{{ value }}</template>
      </span>
    </div>
    <p v-if="sub" class="mt-1.5 truncate text-xs text-fg-muted" :title="sub">{{ sub }}</p>
  </div>
</template>
