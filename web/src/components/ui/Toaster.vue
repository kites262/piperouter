<script setup lang="ts">
import { CircleCheck, CircleX, Info, TriangleAlert, X } from 'lucide-vue-next'

import { useToast, type ToastVariant } from '@/composables/useToast'

const { toasts, dismiss } = useToast()

const icons = {
  info: Info,
  success: CircleCheck,
  warning: TriangleAlert,
  error: CircleX,
} as const

const iconColors: Record<ToastVariant, string> = {
  info: 'text-accent',
  success: 'text-success',
  warning: 'text-warning',
  error: 'text-danger',
}

const barColors: Record<ToastVariant, string> = {
  info: 'bg-accent',
  success: 'bg-success',
  warning: 'bg-warning',
  error: 'bg-danger',
}
</script>

<template>
  <Teleport to="body">
    <div class="pointer-events-none fixed bottom-4 right-4 z-[70] flex w-80 flex-col gap-2" aria-live="polite">
      <TransitionGroup name="toast">
        <div
          v-for="toast in toasts"
          :key="toast.id"
          class="glass pointer-events-auto relative flex items-start gap-2.5 overflow-hidden rounded-lg py-3 pl-4 pr-3 shadow-xl shadow-black/30"
          role="status"
        >
          <span class="absolute inset-y-0 left-0 w-0.5" :class="barColors[toast.variant]" aria-hidden="true" />
          <component :is="icons[toast.variant]" class="mt-0.5 h-4 w-4 shrink-0" :class="iconColors[toast.variant]" />
          <div class="min-w-0 flex-1">
            <p class="text-sm font-medium text-fg">{{ toast.title }}</p>
            <p v-if="toast.message" class="mt-0.5 break-words text-xs text-fg-secondary">{{ toast.message }}</p>
          </div>
          <button
            type="button"
            class="rounded-md p-1 text-fg-muted transition-colors duration-150 hover:bg-surface-raised hover:text-fg"
            aria-label="Dismiss notification"
            @click="dismiss(toast.id)"
          >
            <X class="h-3.5 w-3.5" />
          </button>
        </div>
      </TransitionGroup>
    </div>
  </Teleport>
</template>
