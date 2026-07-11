<!-- Connectivity test dialog — POST /api/v1/diagnostics/transport (PRD §16.2, §19.5). -->
<script setup lang="ts">
import { Check, Minus, X } from 'lucide-vue-next'
import { computed, ref, watch } from 'vue'

import { ApiError, testTransport } from '@/api/client'
import type { DiagnosticsResult, TransportConfig } from '@/api/types'
import Badge from '@/components/ui/Badge.vue'
import Button from '@/components/ui/Button.vue'
import Dialog from '@/components/ui/Dialog.vue'
import Input from '@/components/ui/Input.vue'
import Select from '@/components/ui/Select.vue'

import { formatMs, validateTestUrl, type TransportTestRecord } from './transports'

const open = defineModel<boolean>('open', { required: true })

const props = withDefaults(
  defineProps<{
    /** Transport under test (null while the dialog is closed). */
    transport: TransportConfig | null
    /** Prefill from the previous in-session test of this transport. */
    initialUrl?: string
    initialMethod?: string
  }>(),
  { initialUrl: '', initialMethod: 'GET' },
)

const emit = defineEmits<{
  /** A diagnostics run finished — the parent stores it as the last test result. */
  result: [name: string, record: TransportTestRecord]
}>()

// The diagnostics API only accepts GET/HEAD/POST; other methods 400.
const METHODS = ['GET', 'HEAD', 'POST'] as const

const url = ref('')
const method = ref('GET')
const attempted = ref(false)
const running = ref(false)
const result = ref<DiagnosticsResult | null>(null)
const testedUrl = ref('')
const requestError = ref<string | null>(null)

watch(open, (isOpen) => {
  if (!isOpen) return
  url.value = props.initialUrl
  method.value = props.initialMethod
  attempted.value = false
  result.value = null
  testedUrl.value = ''
  requestError.value = null
})

const urlError = computed(() => validateTestUrl(url.value.trim()))
const showUrlError = computed(() => urlError.value !== null && (attempted.value || url.value !== ''))

async function run(): Promise<void> {
  attempted.value = true
  const transport = props.transport
  if (urlError.value !== null || running.value || transport === null) return

  const target = url.value.trim()
  running.value = true
  requestError.value = null
  try {
    const res = await testTransport({ transport: transport.name, url: target, method: method.value })
    result.value = res
    testedUrl.value = target
    emit('result', transport.name, {
      ok: res.ok,
      status: res.status,
      errorStage: res.error_stage,
      error: res.error,
      headerDurationMs: res.header_duration_ms,
      totalDurationMs: res.total_duration_ms,
      url: target,
      method: method.value,
      testedAt: Date.now(),
    })
  } catch (err) {
    // API-level failure (400 invalid_request, 404 not_found, network) —
    // not a link test result, so nothing is recorded.
    result.value = null
    requestError.value =
      err instanceof ApiError ? (err.detail !== '' ? err.detail : err.code) : String(err)
  } finally {
    running.value = false
  }
}

type StageState = 'ok' | 'fail' | 'skipped' | 'na'
interface StageRow {
  label: string
  state: StageState
  note: string
}

/** Staged view: Connect → TLS → Response (resolve failures surface under Connect). */
const stages = computed<StageRow[]>(() => {
  const res = result.value
  if (res === null) return []
  const errStage = res.error_stage
  const failAt =
    errStage === 'resolve' || errStage === 'connect'
      ? 0
      : errStage === 'tls'
        ? 1
        : errStage === 'response'
          ? 2
          : -1
  const tlsApplies = testedUrl.value.startsWith('https:')

  return [
    {
      label: 'Connect',
      state: failAt === 0 ? 'fail' : 'ok',
      note: errStage === 'resolve' ? 'resolve failed' : failAt === 0 ? 'connect failed' : '',
    },
    {
      label: 'TLS',
      state: !tlsApplies ? 'na' : failAt === 0 ? 'skipped' : failAt === 1 ? 'fail' : 'ok',
      note: !tlsApplies ? 'not applicable (http)' : failAt === 1 ? 'handshake failed' : '',
    },
    {
      label: 'Response',
      state: failAt === 2 ? 'fail' : failAt !== -1 ? 'skipped' : 'ok',
      note:
        res.status > 0
          ? `HTTP ${res.status} · ${formatMs(res.header_duration_ms)}`
          : failAt === 2
            ? 'no response'
            : '',
    },
  ]
})
</script>

<template>
  <Dialog
    v-model:open="open"
    :title="transport !== null ? `Test Transport — ${transport.name}` : 'Test Transport'"
    description="Sends a real request through this transport. Bodies and auth headers are never stored."
    max-width="max-w-xl"
  >
    <form class="space-y-4" novalidate @submit.prevent="run">
      <div class="space-y-1.5">
        <div class="flex gap-2">
          <label class="block w-28 shrink-0 space-y-1.5">
            <span class="block text-xs font-medium text-fg-secondary">Method</span>
            <Select v-model="method">
              <option v-for="m in METHODS" :key="m" :value="m">{{ m }}</option>
            </Select>
          </label>
          <label class="block min-w-0 flex-1 space-y-1.5">
            <span class="block text-xs font-medium text-fg-secondary">Test URL (http / https)</span>
            <Input v-model="url" mono placeholder="https://example.com/health" :invalid="showUrlError" />
          </label>
        </div>
        <p v-if="showUrlError" class="text-xs text-danger">{{ urlError }}</p>
      </div>

      <div
        v-if="requestError !== null"
        class="rounded-md border border-danger/30 bg-danger-soft px-3 py-2 text-xs text-danger"
      >
        {{ requestError }}
      </div>

      <div v-if="result !== null" class="space-y-3">
        <div
          class="flex items-center gap-2 rounded-md border px-3 py-2"
          :class="result.ok ? 'border-success/30 bg-success-soft' : 'border-danger/30 bg-danger-soft'"
        >
          <Check v-if="result.ok" class="h-4 w-4 shrink-0 text-success" />
          <X v-else class="h-4 w-4 shrink-0 text-danger" />
          <span class="text-sm font-medium" :class="result.ok ? 'text-success' : 'text-danger'">
            {{ result.ok ? 'Reachable' : 'Failed' }}
          </span>
          <Badge v-if="result.status > 0" variant="muted" mono>HTTP {{ result.status }}</Badge>
          <span class="ml-auto font-mono text-xs text-fg-secondary">
            {{ formatMs(result.total_duration_ms) }}
          </span>
        </div>

        <ol class="card-flat divide-y divide-border">
          <li v-for="stage in stages" :key="stage.label" class="flex items-center gap-3 px-3 py-2">
            <Check v-if="stage.state === 'ok'" class="h-3.5 w-3.5 shrink-0 text-success" />
            <X v-else-if="stage.state === 'fail'" class="h-3.5 w-3.5 shrink-0 text-danger" />
            <Minus v-else class="h-3.5 w-3.5 shrink-0 text-fg-muted" />
            <span
              class="text-sm"
              :class="
                stage.state === 'fail' ? 'text-danger' : stage.state === 'ok' ? 'text-fg' : 'text-fg-muted'
              "
            >
              {{ stage.label }}
            </span>
            <span v-if="stage.state === 'skipped'" class="text-xs text-fg-muted">skipped</span>
            <span class="ml-auto font-mono text-xs text-fg-muted">{{ stage.note }}</span>
          </li>
        </ol>

        <dl class="card-flat grid grid-cols-[auto_minmax(0,1fr)] gap-x-4 gap-y-1.5 px-3 py-2.5 text-xs">
          <dt class="text-fg-muted">Target URL</dt>
          <dd class="break-all font-mono text-fg-secondary">{{ result.target_url }}</dd>
          <dt class="text-fg-muted">Transport</dt>
          <dd class="font-mono text-fg-secondary">{{ result.transport }}</dd>
          <dt class="text-fg-muted">Headers after</dt>
          <dd class="font-mono text-fg-secondary">{{ formatMs(result.header_duration_ms) }}</dd>
          <dt class="text-fg-muted">Total</dt>
          <dd class="font-mono text-fg-secondary">{{ formatMs(result.total_duration_ms) }}</dd>
          <template v-if="result.error !== ''">
            <dt class="text-fg-muted">Error</dt>
            <dd class="break-all font-mono text-danger">{{ result.error }}</dd>
          </template>
        </dl>
      </div>

      <div class="flex items-center justify-end gap-2 pt-1">
        <Button variant="ghost" :disabled="running" @click="open = false">Close</Button>
        <Button type="submit" :loading="running">Run Test</Button>
      </div>
    </form>
  </Dialog>
</template>
