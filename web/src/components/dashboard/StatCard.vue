<!-- Dashboard stat card — elevated liquid-glass with optional spotlight + tone flash. -->
<script setup lang="ts">
import { ref, watch } from 'vue'

import AnimatedNumber from '@/components/ui/AnimatedNumber.vue'
import StatusDot from '@/components/ui/StatusDot.vue'
import { useSpotlight } from '@/composables/useSpotlight'

const props = withDefaults(
  defineProps<{
    label: string
    /** Static display value (used when `animate` is not provided). */
    value: string
    /** Secondary line under the value. */
    sub?: string
    /** Color of the value text. */
    tone?: 'default' | 'success' | 'warning' | 'danger'
    /** Monospace value — numbers, revisions, durations. */
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

const flashColors = {
  default: 'rgb(109 124 255 / 0.35)',
  success: 'rgb(52 211 153 / 0.4)',
  warning: 'rgb(251 191 36 / 0.45)',
  danger: 'rgb(248 113 113 / 0.5)',
} as const

const root = ref<HTMLElement | null>(null)
useSpotlight(root)

const flash = ref(false)
let flashTimer: ReturnType<typeof setTimeout> | undefined

watch(
  () => props.tone,
  (next, prev) => {
    if (prev === undefined || next === prev) return
    if (next === 'warning' || next === 'danger' || (prev !== 'default' && next === 'success')) {
      flash.value = false
      // re-trigger animation
      requestAnimationFrame(() => {
        flash.value = true
        if (flashTimer) clearTimeout(flashTimer)
        flashTimer = setTimeout(() => {
          flash.value = false
        }, 700)
      })
    }
  },
)
</script>

<template>
  <div
    ref="root"
    class="glass-panel glass-spotlight card-lift p-4"
    :class="flash ? 'tone-flash' : ''"
    :style="flash ? { '--flash-color': flashColors[props.tone] } : undefined"
  >
    <p class="text-[11px] font-medium uppercase tracking-widest text-fg-muted">{{ label }}</p>
    <div class="mt-2 flex min-w-0 items-center gap-2">
      <StatusDot v-if="dot" :status="dot" />
      <span
        class="truncate text-xl font-semibold leading-tight tracking-tight"
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
