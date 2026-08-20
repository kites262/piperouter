<script setup lang="ts">
import { ref, watch } from 'vue'

import { ApiError, getLogDetailValue, getLogDetails } from '@/api/client'
import type { AccessLogEntry, LogDetails } from '@/api/types'
import Button from '@/components/ui/Button.vue'
import Dialog from '@/components/ui/Dialog.vue'
import DetailValue from '@/components/logs/DetailValue.vue'

const props = defineProps<{ entry: AccessLogEntry | null }>()
const open = defineModel<boolean>('open', { required: true })

const details = ref<LogDetails | null>(null)
const loading = ref(false)
const error = ref('')
const valueOpen = ref(false)
const valueLoading = ref(false)
const valueError = ref('')
const fullValue = ref('')
const valueRef = ref('')

watch(
  () => [open.value, props.entry?.details_id] as const,
  ([isOpen]) => {
    if (isOpen) void loadDetails()
  },
)

async function loadDetails(): Promise<void> {
  if (!props.entry?.details_id) return
  loading.value = true
  error.value = ''
  details.value = null
  try {
    details.value = await getLogDetails(props.entry.details_id)
  } catch (err) {
    error.value = err instanceof ApiError ? err.message : 'Failed to load request details'
  } finally {
    loading.value = false
  }
}

async function showValue(refName: string): Promise<void> {
  if (!props.entry?.details_id) return
  valueRef.value = refName
  valueOpen.value = true
  valueLoading.value = true
  valueError.value = ''
  fullValue.value = ''
  try {
    fullValue.value = await getLogDetailValue(props.entry.details_id, refName)
  } catch (err) {
    valueError.value = err instanceof ApiError ? err.message : 'Failed to load full value'
  } finally {
    valueLoading.value = false
  }
}

function formatHeaders(value: Record<string, string[]> | undefined): string {
  return JSON.stringify(value ?? {}, null, 2)
}
</script>

<template>
  <Dialog v-model:open="open" title="Request details" description="Captured request and response data" max-width="max-w-4xl">
    <div v-if="loading" class="py-10 text-center text-sm text-fg-muted">Loading details…</div>
    <div v-else-if="error" class="rounded-md border border-danger/30 bg-danger-soft p-3 text-sm text-danger">
      {{ error }}
    </div>
    <div v-else-if="details" class="space-y-5 font-mono text-xs">
      <dl class="grid gap-2 sm:grid-cols-2">
        <div><dt class="text-fg-muted">Method</dt><dd class="text-fg">{{ details.method }}</dd></div>
        <div><dt class="text-fg-muted">Status</dt><dd class="text-fg">{{ details.status || '—' }}</dd></div>
        <div class="sm:col-span-2"><dt class="text-fg-muted">Request URL</dt><dd class="break-all text-fg">{{ details.request_url }}</dd></div>
        <div class="sm:col-span-2"><dt class="text-fg-muted">Outbound URL</dt><dd class="break-all text-fg">{{ details.outbound_url || '—' }}</dd></div>
        <div><dt class="text-fg-muted">Route</dt><dd class="text-fg">{{ details.route || '—' }}</dd></div>
        <div><dt class="text-fg-muted">Transport</dt><dd class="text-fg">{{ details.transport || '—' }}</dd></div>
      </dl>

      <section>
        <h3 class="mb-1 text-xs font-semibold text-fg">Request headers</h3>
        <pre class="max-h-48 overflow-auto whitespace-pre-wrap break-words rounded bg-bg-deep p-3 text-fg-secondary">{{ formatHeaders(details.request_headers) }}</pre>
      </section>
      <section v-if="details.outbound_headers">
        <h3 class="mb-1 text-xs font-semibold text-fg">Outbound headers</h3>
        <pre class="max-h-48 overflow-auto whitespace-pre-wrap break-words rounded bg-bg-deep p-3 text-fg-secondary">{{ formatHeaders(details.outbound_headers) }}</pre>
      </section>
      <section>
        <h3 class="mb-1 text-xs font-semibold text-fg">Request body ({{ details.request_body_bytes }} bytes)</h3>
        <div class="max-h-80 overflow-auto rounded bg-bg-deep p-3 text-fg-secondary">
          <DetailValue :value="details.request_body" @long-string="showValue" />
        </div>
      </section>
      <section>
        <h3 class="mb-1 text-xs font-semibold text-fg">Response body ({{ details.response_body_bytes }} bytes)</h3>
        <div class="max-h-[40rem] overflow-auto rounded bg-bg-deep p-3 text-fg-secondary">
          <DetailValue :value="details.response_body" @long-string="showValue" />
        </div>
      </section>
      <p v-if="details.request_body_truncated || details.response_body_truncated" class="text-warning">
        Body capture reached its configured limit and is truncated.
      </p>
    </div>
    <template #footer>
      <Button variant="outline" @click="open = false">Close</Button>
    </template>
  </Dialog>

  <Dialog v-model:open="valueOpen" :title="`Full value · ${valueRef}`" max-width="max-w-4xl">
    <div v-if="valueLoading" class="py-10 text-center text-sm text-fg-muted">Loading value…</div>
    <p v-else-if="valueError" class="text-sm text-danger">{{ valueError }}</p>
    <pre v-else class="max-h-[70vh] overflow-auto whitespace-pre-wrap break-words rounded bg-bg-deep p-3 font-mono text-xs leading-relaxed text-fg-secondary">{{ fullValue }}</pre>
  </Dialog>
</template>
