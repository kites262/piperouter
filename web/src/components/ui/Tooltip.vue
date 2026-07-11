<script setup lang="ts">
/**
 * Hover/focus tooltip.
 *
 * The popup is position:fixed (not absolute) so it never contributes to an
 * ancestor's scrollable overflow — absolute+nowrap tooltips were the cause of
 * phantom horizontal scrollbars under table overflow containers.
 */
import { computed, nextTick, onBeforeUnmount, ref, watch } from 'vue'

defineProps<{ text: string }>()

const open = ref(false)
const anchor = ref<HTMLElement | null>(null)
const tip = ref<HTMLElement | null>(null)
const pos = ref({ top: 0, left: 0 })

function place(): void {
  const el = anchor.value
  if (!el) return
  const r = el.getBoundingClientRect()
  // Prefer centered above the anchor; clamp horizontally to the viewport.
  const tipW = tip.value?.offsetWidth ?? 0
  const tipH = tip.value?.offsetHeight ?? 0
  let left = r.left + r.width / 2 - tipW / 2
  left = Math.max(8, Math.min(left, window.innerWidth - tipW - 8))
  let top = r.top - tipH - 8
  if (top < 8) {
    // Flip below when there is no room above.
    top = r.bottom + 8
  }
  pos.value = { top, left }
}

async function show(): Promise<void> {
  open.value = true
  await nextTick()
  place()
  // Second pass after the tip has real dimensions.
  await nextTick()
  place()
}

function hide(): void {
  open.value = false
}

function onScrollOrResize(): void {
  if (open.value) place()
}

watch(open, (v) => {
  if (v) {
    window.addEventListener('scroll', onScrollOrResize, true)
    window.addEventListener('resize', onScrollOrResize)
  } else {
    window.removeEventListener('scroll', onScrollOrResize, true)
    window.removeEventListener('resize', onScrollOrResize)
  }
})

onBeforeUnmount(() => {
  window.removeEventListener('scroll', onScrollOrResize, true)
  window.removeEventListener('resize', onScrollOrResize)
})

const style = computed(() => ({
  top: `${pos.value.top}px`,
  left: `${pos.value.left}px`,
}))
</script>

<template>
  <span
    ref="anchor"
    class="inline-flex max-w-full min-w-0 items-center justify-center"
    @mouseenter="show"
    @mouseleave="hide"
    @focusin="show"
    @focusout="hide"
  >
    <slot />
    <Teleport to="body">
      <span
        v-if="open && text"
        ref="tip"
        class="pointer-events-none fixed z-[80] max-w-[min(20rem,calc(100vw-1rem))] rounded-md border border-border bg-surface-raised px-2 py-1 text-xs text-fg shadow-lg shadow-black/30"
        :style="style"
        role="tooltip"
      >
        {{ text }}
      </span>
    </Teleport>
  </span>
</template>
