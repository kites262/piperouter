<script setup lang="ts">
import { computed } from 'vue'

const props = withDefaults(
  defineProps<{
    variant?: 'default' | 'accent' | 'success' | 'warning' | 'danger' | 'muted'
    /** Monospace content — for revisions, prefixes, codes. */
    mono?: boolean
  }>(),
  { variant: 'default', mono: false },
)

const variants = {
  default: 'border-border bg-surface/40 text-fg-secondary backdrop-blur-sm',
  accent: 'border-accent/30 bg-accent-soft text-accent shadow-[0_0_12px_-4px_rgb(109_124_255_/_0.4)]',
  success: 'border-success/30 bg-success-soft text-success',
  warning: 'border-warning/30 bg-warning-soft text-warning',
  danger: 'border-danger/30 bg-danger-soft text-danger',
  muted: 'border-border bg-surface-raised text-fg-muted',
} as const

const classes = computed(() => [variants[props.variant], props.mono ? 'font-mono' : ''])
</script>

<template>
  <span
    class="inline-flex items-center gap-1 whitespace-nowrap rounded-full border px-2 py-0.5 text-xs font-medium"
    :class="classes"
  >
    <slot />
  </span>
</template>
