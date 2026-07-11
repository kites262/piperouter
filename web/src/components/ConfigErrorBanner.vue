<script setup lang="ts">
import { ChevronDown, RefreshCw, TriangleAlert } from 'lucide-vue-next'
import { computed, inject, ref } from 'vue'
import { RouterLink } from 'vue-router'

import { getStatus } from '@/api/client'
import type { StatusResponse } from '@/api/types'
import Button from '@/components/ui/Button.vue'
import { usePolling } from '@/composables/usePolling'
import { statusKey, type StatusState } from '@/composables/useStatus'

// Reuse the AppShell status poll when provided; otherwise poll on our own,
// so the banner also works standalone (PRD §19.9).
const injected = inject(statusKey, undefined)

let state: StatusState
if (injected !== undefined) {
  state = injected
} else {
  const status = ref<StatusResponse | null>(null)
  const error = ref<string | null>(null)
  const { refresh } = usePolling(
    async () => {
      try {
        status.value = await getStatus()
        error.value = null
      } catch (err) {
        error.value = err instanceof Error ? err.message : String(err)
      }
    },
    { interval: 5000 },
  )
  state = { status, error, refresh }
}

const status = state.status
const expanded = ref(false)
const reloading = ref(false)

const invalid = computed(() => status.value !== null && !status.value.config.valid)
const lastError = computed(() => status.value?.config.last_error ?? '')
const revision = computed(() => {
  const rev = status.value?.config.revision ?? ''
  return rev.startsWith('sha256:') ? rev.slice(7, 19) : rev
})

async function reload(): Promise<void> {
  reloading.value = true
  try {
    await state.refresh()
  } finally {
    reloading.value = false
  }
}
</script>

<template>
  <Transition name="banner">
    <div
      v-if="invalid"
      class="shrink-0 border-b border-warning/40 bg-warning-soft px-5 py-3"
      role="alert"
    >
      <div class="flex flex-wrap items-center gap-x-4 gap-y-2 text-sm">
        <div class="flex min-w-0 items-center gap-2.5">
          <TriangleAlert class="h-4 w-4 shrink-0 text-warning" />
          <p class="font-medium text-warning">
            Configuration invalid. PipeRouter is still running with the last valid configuration.
          </p>
        </div>
        <span v-if="revision" class="font-mono text-xs text-fg-muted">active revision {{ revision }}</span>
        <div class="ml-auto flex items-center gap-2">
          <button
            v-if="lastError"
            type="button"
            class="inline-flex items-center gap-1 rounded-md px-2 py-1 text-xs text-fg-secondary transition-colors duration-150 hover:bg-surface-raised hover:text-fg"
            :aria-expanded="expanded"
            @click="expanded = !expanded"
          >
            Details
            <ChevronDown
              class="h-3.5 w-3.5 transition-transform duration-150"
              :class="expanded ? 'rotate-180' : ''"
            />
          </button>
          <Button size="sm" variant="outline" :loading="reloading" @click="reload">
            <RefreshCw v-if="!reloading" class="h-3.5 w-3.5" />
            Reload
          </Button>
          <RouterLink
            to="/settings"
            class="text-xs font-medium text-accent underline-offset-2 transition-colors duration-150 hover:text-accent-hover hover:underline"
          >
            Open Settings
          </RouterLink>
        </div>
      </div>
      <pre
        v-if="expanded && lastError"
        class="card-flat mt-3 overflow-x-auto p-3 font-mono text-xs leading-relaxed text-danger"
      >{{ lastError }}</pre>
    </div>
  </Transition>
</template>
