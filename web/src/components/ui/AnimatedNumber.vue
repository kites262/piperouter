<!-- Tweens a numeric value when it changes (easeOutCubic), rendering the
     formatted intermediate. Snaps instantly under prefers-reduced-motion.
     Digits use tabular figures so the width does not jitter mid-tween. -->
<script setup lang="ts">
import { onBeforeUnmount, ref, watch } from 'vue'

const props = withDefaults(
  defineProps<{
    value: number
    /** Tween duration in ms. */
    duration?: number
    /** Optional formatter; defaults to a rounded integer. */
    format?: (n: number) => string
  }>(),
  { duration: 600 },
)

const reduceMotion =
  typeof window !== 'undefined' && window.matchMedia('(prefers-reduced-motion: reduce)').matches

const display = ref(props.value)
let raf = 0

function render(n: number): string {
  return props.format ? props.format(n) : String(Math.round(n))
}

function tween(to: number): void {
  cancelAnimationFrame(raf)
  const from = display.value
  if (reduceMotion || from === to) {
    display.value = to
    return
  }
  const start = performance.now()
  const step = (now: number): void => {
    const t = Math.min(1, (now - start) / props.duration)
    const eased = 1 - Math.pow(1 - t, 3)
    display.value = from + (to - from) * eased
    if (t < 1) raf = requestAnimationFrame(step)
    else display.value = to
  }
  raf = requestAnimationFrame(step)
}

watch(
  () => props.value,
  (v) => tween(v),
)
onBeforeUnmount(() => cancelAnimationFrame(raf))
</script>

<template>
  <span class="tnums">{{ render(display) }}</span>
</template>
