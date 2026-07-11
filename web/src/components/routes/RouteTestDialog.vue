<script setup lang="ts">
import { CheckCircle2, Circle, Info, Minus, Timer, XCircle } from 'lucide-vue-next'
import { computed, ref, watch, type Component } from 'vue'

import { ApiError, testRoute } from '@/api/client'
import type { DiagnosticsResult, RouteConfig } from '@/api/types'
import Badge from '@/components/ui/Badge.vue'
import Button from '@/components/ui/Button.vue'
import Dialog from '@/components/ui/Dialog.vue'
import Input from '@/components/ui/Input.vue'
import Select from '@/components/ui/Select.vue'
import { useToast } from '@/composables/useToast'

/**
 * "Test Route" dialog (PRD §19.4 + §19.7): sends a real request through the
 * route's pipeline via POST /api/v1/diagnostics/route and renders the result
 * stage by stage (Route Resolution → Target URL → Transport Selection →
 * Connection → TLS → HTTP Response → Duration).
 */
const open = defineModel<boolean>('open', { required: true })

const props = defineProps<{ route: RouteConfig }>()

const toast = useToast()

const path = ref('/')
const method = ref('GET')
const running = ref(false)
const result = ref<DiagnosticsResult | null>(null)
const requestError = ref('')

const pathInvalid = computed(() => path.value === '' || !path.value.startsWith('/'))

// Fresh result on every open; keep the last-used path/method for convenience.
watch(open, (isOpen) => {
  if (isOpen) {
    result.value = null
    requestError.value = ''
  }
})

async function run(): Promise<void> {
  if (pathInvalid.value || running.value) return
  running.value = true
  requestError.value = ''
  try {
    result.value = await testRoute({
      route: props.route.name,
      path: path.value,
      method: method.value,
    })
  } catch (err) {
    result.value = null
    const message =
      err instanceof ApiError ? (err.detail !== '' ? err.detail : err.code) : String(err)
    requestError.value = message
    toast.error('Route test failed', { message })
  } finally {
    running.value = false
  }
}

// --- staged result model ---------------------------------------------------

type StageState = 'ok' | 'fail' | 'skip' | 'wait'

interface StageRow {
  key: string
  label: string
  state: StageState
  value: string
  note: string
}

const STAGE_ORDER = ['resolve', 'connect', 'tls', 'response'] as const
const STAGE_LABELS: Record<(typeof STAGE_ORDER)[number], string> = {
  resolve: 'Route Resolution',
  connect: 'Connection',
  tls: 'TLS',
  response: 'HTTP Response',
}

const failStageLabel = computed(() => {
  const r = result.value
  if (r === null || r.error_stage === '') return ''
  return STAGE_LABELS[r.error_stage]
})

const stageRows = computed<StageRow[]>(() => {
  const r = result.value
  if (r === null) return []
  const failIdx = r.error_stage === '' ? Number.POSITIVE_INFINITY : STAGE_ORDER.indexOf(r.error_stage)
  const httpsTarget = r.target_url.startsWith('https:')
  const failNote = r.error !== '' ? r.error : 'failed'

  function stateFor(idx: number): StageState {
    if (failIdx === idx) return 'fail'
    if (failIdx < idx) return 'wait'
    return 'ok'
  }
  function noteFor(state: StageState): string {
    if (state === 'fail') return failNote
    if (state === 'wait') return 'not reached'
    return ''
  }

  const resolveState = stateFor(0)
  const connectState = stateFor(1)
  let tlsState = stateFor(2)
  let tlsNote = noteFor(tlsState)
  if (tlsState === 'ok' && !httpsTarget) {
    tlsState = 'skip'
    tlsNote = 'not used — plain HTTP target'
  }
  const responseState = stateFor(3)

  return [
    {
      key: 'resolve',
      label: 'Route Resolution',
      state: resolveState,
      value: resolveState === 'ok' ? 'matched' : '',
      note: noteFor(resolveState),
    },
    {
      key: 'target',
      label: 'Target URL',
      state: resolveState === 'ok' ? 'ok' : 'wait',
      value: r.target_url !== '' ? r.target_url : '—',
      note: '',
    },
    {
      key: 'transport',
      label: 'Transport Selection',
      state: resolveState === 'ok' ? 'ok' : 'wait',
      value: r.transport !== '' ? r.transport : '—',
      note: '',
    },
    {
      key: 'connect',
      label: 'Connection',
      state: connectState,
      value: connectState === 'ok' ? 'connected' : '',
      note: noteFor(connectState),
    },
    {
      key: 'tls',
      label: 'TLS',
      state: tlsState,
      value: tlsState === 'ok' ? 'handshake ok' : '',
      note: tlsNote,
    },
    {
      key: 'response',
      label: 'HTTP Response',
      state: responseState,
      value: '',
      note: noteFor(responseState),
    },
  ]
})

/** 401/403/404 still means the link works (PRD §16.1) — surface that. */
const authHint = computed(() => {
  const r = result.value
  return r !== null && (r.status === 401 || r.status === 403 || r.status === 404)
})

const stateIcons: Record<StageState, Component> = {
  ok: CheckCircle2,
  fail: XCircle,
  skip: Minus,
  wait: Circle,
}

const stateColors: Record<StageState, string> = {
  ok: 'text-success',
  fail: 'text-danger',
  skip: 'text-fg-muted',
  wait: 'text-fg-muted',
}

function statusVariant(status: number): 'success' | 'accent' | 'warning' | 'danger' | 'muted' {
  if (status >= 200 && status < 300) return 'success'
  if (status >= 300 && status < 400) return 'accent'
  if (status >= 400 && status < 500) return 'warning'
  if (status >= 500) return 'danger'
  return 'muted'
}

function fmtMs(ms: number): string {
  if (!Number.isFinite(ms)) return '—'
  if (ms >= 1000) return `${(ms / 1000).toFixed(2)} s`
  if (ms >= 100) return `${Math.round(ms)} ms`
  return `${ms.toFixed(1)} ms`
}
</script>

<template>
  <Dialog
    v-model:open="open"
    title="Test Route"
    :description="`Send a real request through “${route.name}” and inspect each pipeline stage.`"
    max-width="max-w-xl"
  >
    <form class="flex flex-col gap-3 sm:flex-row sm:items-end" @submit.prevent="run">
      <div class="flex-1 space-y-1.5">
        <label for="route-test-path" class="block text-xs font-medium uppercase tracking-wide text-fg-muted">
          Path
        </label>
        <Input id="route-test-path" v-model="path" mono placeholder="/" :invalid="pathInvalid" />
      </div>
      <div class="w-full space-y-1.5 sm:w-32">
        <label for="route-test-method" class="block text-xs font-medium uppercase tracking-wide text-fg-muted">
          Method
        </label>
        <Select id="route-test-method" v-model="method">
          <option value="GET">GET</option>
          <option value="HEAD">HEAD</option>
          <option value="POST">POST</option>
        </Select>
      </div>
      <Button type="submit" :loading="running" :disabled="pathInvalid">Run test</Button>
    </form>

    <p v-if="pathInvalid" class="mt-2 text-xs text-danger">Path must start with “/”.</p>

    <p
      v-if="!route.enabled"
      class="mt-3 rounded-md border border-warning/30 bg-warning-soft px-3 py-2 text-xs text-warning"
    >
      This route is disabled — route resolution will fail until it is enabled.
    </p>

    <div
      v-if="requestError"
      class="mt-3 rounded-md border border-danger/30 bg-danger-soft px-3 py-2 text-xs text-danger"
    >
      {{ requestError }}
    </div>

    <div v-if="running && result === null" class="mt-4 space-y-2" aria-busy="true">
      <div v-for="i in 4" :key="i" class="h-9 animate-pulse rounded-md bg-surface-raised/70" />
    </div>

    <div v-else-if="result" class="mt-4 space-y-3">
      <!-- Overall verdict -->
      <div
        class="flex items-center gap-2 rounded-md border px-3 py-2 text-sm"
        :class="
          result.ok
            ? 'border-success/30 bg-success-soft text-success'
            : 'border-danger/30 bg-danger-soft text-danger'
        "
      >
        <CheckCircle2 v-if="result.ok" class="h-4 w-4 shrink-0" />
        <XCircle v-else class="h-4 w-4 shrink-0" />
        <span class="font-medium">
          {{ result.ok ? 'Route link OK' : `Test failed at ${failStageLabel || 'unknown stage'}` }}
        </span>
      </div>

      <!-- Per-stage breakdown -->
      <ul class="card-flat divide-y divide-border overflow-hidden">
        <li v-for="row in stageRows" :key="row.key" class="flex items-start gap-3 px-3 py-2.5">
          <component
            :is="stateIcons[row.state]"
            class="mt-0.5 h-4 w-4 shrink-0"
            :class="stateColors[row.state]"
          />
          <div class="min-w-0 flex-1">
            <div class="flex flex-wrap items-center justify-between gap-x-3 gap-y-1">
              <span class="text-sm text-fg">{{ row.label }}</span>
              <Badge
                v-if="row.key === 'response' && result.status > 0"
                :variant="statusVariant(result.status)"
                mono
              >
                {{ result.status }}
              </Badge>
              <span
                v-else-if="row.value"
                class="min-w-0 truncate font-mono text-xs text-fg-secondary"
                :title="row.value"
              >
                {{ row.value }}
              </span>
            </div>
            <p
              v-if="row.note"
              class="mt-0.5 text-xs"
              :class="row.state === 'fail' ? 'text-danger' : 'text-fg-muted'"
            >
              {{ row.note }}
            </p>
          </div>
        </li>
        <li class="flex items-center gap-3 px-3 py-2.5">
          <Timer class="h-4 w-4 shrink-0 text-fg-muted" />
          <span class="flex-1 text-sm text-fg">Duration</span>
          <span class="font-mono text-xs text-fg-secondary">
            headers {{ fmtMs(result.header_duration_ms) }} · total {{ fmtMs(result.total_duration_ms) }}
          </span>
        </li>
      </ul>

      <!-- 401/403/404 still means the link itself works -->
      <div
        v-if="authHint"
        class="flex items-start gap-2 rounded-md border border-accent/30 bg-accent-soft px-3 py-2 text-xs text-fg-secondary"
      >
        <Info class="mt-0.5 h-3.5 w-3.5 shrink-0 text-accent" />
        <span>
          HTTP {{ result.status }} from the upstream still means the link is OK — the target
          requires credentials or the test path does not exist on it.
        </span>
      </div>
    </div>

    <template #footer>
      <Button variant="outline" @click="open = false">Close</Button>
    </template>
  </Dialog>
</template>
