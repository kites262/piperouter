<script setup lang="ts">
import { X } from 'lucide-vue-next'
import { nextTick, onBeforeUnmount, ref, watch } from 'vue'

const open = defineModel<boolean>('open', { required: true })

withDefaults(
  defineProps<{
    title?: string
    description?: string
    /** Tailwind max-width class for the panel. */
    maxWidth?: string
  }>(),
  { title: '', description: '', maxWidth: 'max-w-lg' },
)

const panel = ref<HTMLElement | null>(null)
let lastFocused: HTMLElement | null = null

const FOCUSABLE =
  'a[href], button:not([disabled]), input:not([disabled]), select:not([disabled]), textarea:not([disabled]), [tabindex]:not([tabindex="-1"])'

function close(): void {
  open.value = false
}

function trapFocus(event: KeyboardEvent): void {
  const root = panel.value
  if (!root) return
  const nodes = Array.from(root.querySelectorAll<HTMLElement>(FOCUSABLE))
  if (nodes.length === 0) {
    event.preventDefault()
    root.focus()
    return
  }
  const first = nodes[0]
  const last = nodes[nodes.length - 1]
  const active = document.activeElement
  const inside = active instanceof HTMLElement && root.contains(active)
  if (event.shiftKey) {
    if (!inside || active === first) {
      event.preventDefault()
      last?.focus()
    }
  } else if (!inside || active === last) {
    event.preventDefault()
    first?.focus()
  }
}

function onKeydown(event: KeyboardEvent): void {
  if (!open.value) return
  if (event.key === 'Escape') {
    event.stopPropagation()
    close()
    return
  }
  if (event.key === 'Tab') trapFocus(event)
}

watch(open, async (isOpen) => {
  if (isOpen) {
    lastFocused = document.activeElement instanceof HTMLElement ? document.activeElement : null
    document.addEventListener('keydown', onKeydown, true)
    await nextTick()
    const root = panel.value
    const target = root?.querySelector<HTMLElement>(FOCUSABLE) ?? root
    target?.focus()
  } else {
    document.removeEventListener('keydown', onKeydown, true)
    lastFocused?.focus()
    lastFocused = null
  }
})

onBeforeUnmount(() => {
  document.removeEventListener('keydown', onKeydown, true)
})
</script>

<template>
  <Teleport to="body">
    <Transition name="dialog">
      <div v-if="open" class="fixed inset-0 z-50 flex items-center justify-center p-4">
        <div class="absolute inset-0 bg-black/60 backdrop-blur-sm" aria-hidden="true" @click="close" />
        <div
          ref="panel"
          role="dialog"
          aria-modal="true"
          tabindex="-1"
          class="glass relative max-h-[85vh] w-full overflow-y-auto rounded-xl p-5 shadow-2xl shadow-black/40 focus:outline-none"
          :class="maxWidth"
        >
          <header v-if="title || description || $slots.header" class="mb-4 flex items-start justify-between gap-4">
            <div class="min-w-0">
              <h2 v-if="title" class="text-base font-semibold text-fg">{{ title }}</h2>
              <p v-if="description" class="mt-1 text-sm text-fg-muted">{{ description }}</p>
              <slot name="header" />
            </div>
            <button
              type="button"
              class="rounded-md p-1 text-fg-muted transition-colors duration-150 hover:bg-surface-raised hover:text-fg"
              aria-label="Close dialog"
              @click="close"
            >
              <X class="h-4 w-4" />
            </button>
          </header>
          <slot />
          <footer v-if="$slots.footer" class="mt-5 flex items-center justify-end gap-2">
            <slot name="footer" />
          </footer>
        </div>
      </div>
    </Transition>
  </Teleport>
</template>
