<script setup lang="ts">
import { computed } from 'vue'

const props = withDefaults(
  defineProps<{
    status?: 'success' | 'warning' | 'danger' | 'accent' | 'muted'
    /** Breathing animation (auto-disabled by prefers-reduced-motion). */
    pulse?: boolean
  }>(),
  { status: 'muted', pulse: true },
)

const colors = {
  success: 'bg-success shadow-[0_0_6px] shadow-success/60',
  warning: 'bg-warning shadow-[0_0_6px] shadow-warning/60',
  danger: 'bg-danger shadow-[0_0_6px] shadow-danger/60',
  accent: 'bg-accent shadow-[0_0_6px] shadow-accent/60',
  muted: 'bg-fg-muted',
} as const

const classes = computed(() => [colors[props.status], props.pulse ? 'animate-breathe' : ''])
</script>

<template>
  <span class="inline-flex h-2 w-2 shrink-0 rounded-full" :class="classes" aria-hidden="true" />
</template>
