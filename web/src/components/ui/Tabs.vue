<script setup lang="ts">
import { nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import type { ComponentPublicInstance } from 'vue'

const model = defineModel<string>({ required: true })

const props = defineProps<{
  tabs: Array<{ value: string; label: string }>
}>()

const itemEls = ref<HTMLElement[]>([])
const indicator = ref({ left: 0, width: 0, visible: false })

function captureItem(el: Element | ComponentPublicInstance | null, index: number): void {
  if (!el) return
  const node = ('$el' in el ? (el as ComponentPublicInstance).$el : el) as unknown
  if (node instanceof HTMLElement) itemEls.value[index] = node
}

function updateIndicator(): void {
  const idx = props.tabs.findIndex((t) => t.value === model.value)
  const el = idx >= 0 ? itemEls.value[idx] : undefined
  if (!el) {
    indicator.value = { ...indicator.value, visible: false }
    return
  }
  indicator.value = {
    left: el.offsetLeft,
    width: el.offsetWidth,
    visible: true,
  }
}

watch(model, () => void nextTick(updateIndicator))
watch(
  () => props.tabs,
  () => void nextTick(updateIndicator),
  { deep: true },
)
onMounted(() => {
  void nextTick(updateIndicator)
  window.addEventListener('resize', updateIndicator)
})
onBeforeUnmount(() => window.removeEventListener('resize', updateIndicator))
</script>

<template>
  <div
    class="relative inline-flex items-center gap-1 rounded-xl border border-border p-1"
    role="tablist"
    style="background: var(--toolbar-bg); backdrop-filter: blur(12px) saturate(1.3)"
  >
    <span
      class="nav-indicator pointer-events-none absolute top-1 z-0 h-[calc(100%-8px)] rounded-md bg-accent-soft"
      :class="indicator.visible ? 'opacity-100' : 'opacity-0'"
      :style="{
        transform: `translateX(${indicator.left}px)`,
        width: `${indicator.width}px`,
        boxShadow: 'inset 0 0 0 1px color-mix(in srgb, var(--color-accent) 25%, transparent)',
      }"
      aria-hidden="true"
    />
    <button
      v-for="(tab, i) in tabs"
      :key="tab.value"
      :ref="(el) => captureItem(el as Element | ComponentPublicInstance | null, i)"
      type="button"
      role="tab"
      :aria-selected="model === tab.value"
      class="relative z-10 rounded-md px-3 py-1.5 text-sm transition-colors duration-150"
      :class="
        model === tab.value
          ? 'font-medium text-accent'
          : 'text-fg-secondary hover:text-fg'
      "
      @click="model = tab.value"
    >
      {{ tab.label }}
    </button>
  </div>
</template>
