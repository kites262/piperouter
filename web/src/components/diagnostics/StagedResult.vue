<!-- Shared staged diagnostics result.
     Vertical stage list with staggered enter + glass shell. -->
<script setup lang="ts">
import { Check, Info, Minus, X } from 'lucide-vue-next'
import { computed } from 'vue'

import Badge from '@/components/ui/Badge.vue'
import { formatMs, statusTone, type DiagnosticsRun } from './runs'

const props = defineProps<{ run: DiagnosticsRun }>()

type StageState = 'ok' | 'fail' | 'skip'

interface Stage {
  key: string
  label: string
  state: StageState
  value?: string
  note?: string
  error?: string
  status?: number
  hint?: string
}

const FAIL_GROUPS: Record<string, number> = { resolve: -1, connect: 0, tls: 1, response: 2 }

const KIND_LABEL: Record<DiagnosticsRun['kind'], string> = {
  request: 'Request probe',
  route: 'Route test',
  transport: 'Transport test',
}

const result = computed(() => props.run.result)

const targetUrl = computed(() => {
  if (result.value.target_url !== '') return result.value.target_url
  return props.run.kind === 'transport' ? props.run.input : ''
})

const matchedRoute = computed(() => {
  if (result.value.route !== '') return result.value.route
  if (props.run.kind === 'route') return props.run.subject
  return ''
})

const isHttps = computed(() => targetUrl.value.toLowerCase().startsWith('https:'))

const stages = computed<Stage[]>(() => {
  const r = result.value
  const failGroup =
    r.error_stage === '' ? Number.POSITIVE_INFINITY : (FAIL_GROUPS[r.error_stage] ?? 0)
  const net = (group: number): StageState =>
    group < failGroup ? 'ok' : group === failGroup ? 'fail' : 'skip'

  const list: Stage[] = []

  if (props.run.kind === 'transport') {
    list.push({
      key: 'route',
      label: 'Route Resolution',
      state: 'skip',
      note: 'not applicable for transport tests',
    })
  } else {
    const resolveFailed = r.error_stage === 'resolve'
    const name = matchedRoute.value
    list.push({
      key: 'route',
      label: 'Route Resolution',
      state: resolveFailed ? 'fail' : 'ok',
      value: resolveFailed ? undefined : name !== '' ? name : undefined,
      note: resolveFailed
        ? undefined
        : props.run.kind === 'request'
          ? 'longest-prefix match on the inbound path (data plane)'
          : 'named route resolved and rewritten through the real pipeline',
      error: resolveFailed ? r.error : undefined,
    })
  }

  list.push({
    key: 'target',
    label: 'Target URL',
    state: targetUrl.value !== '' ? 'ok' : 'skip',
    value: targetUrl.value !== '' ? targetUrl.value : undefined,
  })

  list.push({
    key: 'transport',
    label: 'Transport Selection',
    state: r.transport !== '' ? 'ok' : 'skip',
    value: r.transport !== '' ? r.transport : undefined,
  })

  const connState = net(0)
  list.push({
    key: 'connect',
    label: 'Connection',
    state: connState,
    note: connState === 'ok' ? 'connection established' : undefined,
    error: connState === 'fail' ? r.error : undefined,
  })

  if (isHttps.value) {
    const tlsState = net(1)
    list.push({
      key: 'tls',
      label: 'TLS',
      state: tlsState,
      note: tlsState === 'ok' ? 'handshake completed' : undefined,
      error: tlsState === 'fail' ? r.error : undefined,
    })
  }

  const respState = net(2)
  list.push({
    key: 'response',
    label: 'HTTP Response',
    state: respState,
    status: r.status > 0 ? r.status : undefined,
    error: respState === 'fail' ? r.error : undefined,
    hint:
      r.status === 401 || r.status === 403 || r.status === 404
        ? `HTTP ${r.status} still means the network link was established successfully — ` +
          'the target is reachable; it only rejected the request or does not know this path.'
        : undefined,
  })

  list.push({
    key: 'duration',
    label: 'Duration',
    state: r.ok ? 'ok' : 'skip',
    value:
      r.status > 0
        ? `header ${formatMs(r.header_duration_ms)} · total ${formatMs(r.total_duration_ms)}`
        : `total ${formatMs(r.total_duration_ms)}`,
  })

  return list
})

const iconClasses: Record<StageState, string> = {
  ok: 'border-success/40 bg-success-soft text-success shadow-[0_0_12px_-4px_rgb(52_211_153_/_0.45)]',
  fail: 'border-danger/40 bg-danger-soft text-danger shadow-[0_0_12px_-4px_rgb(248_113_113_/_0.45)]',
  skip: 'border-border bg-surface-raised text-fg-muted',
}

const lineClasses: Record<StageState, string> = {
  ok: 'bg-gradient-to-b from-success/50 to-border',
  fail: 'bg-danger/40',
  skip: 'bg-border',
}
</script>

<template>
  <article class="glass-panel animate-fade-scale p-4">
    <header class="flex flex-wrap items-center justify-between gap-3 border-b border-border pb-3">
      <div class="flex min-w-0 flex-wrap items-center gap-2">
        <Badge :variant="run.kind === 'request' ? 'accent' : run.kind === 'route' ? 'default' : 'muted'">
          {{ KIND_LABEL[run.kind] }}
        </Badge>
        <span class="font-mono text-xs text-fg-secondary">{{ run.method }}</span>
        <span class="min-w-0 max-w-full truncate font-mono text-xs text-fg" :title="run.input">
          <template v-if="run.kind === 'route'">{{ run.subject }} · {{ run.input }}</template>
          <template v-else-if="run.kind === 'request'">
            {{ run.input }}
            <template v-if="matchedRoute !== ''"> · {{ matchedRoute }}</template>
          </template>
          <template v-else>{{ run.subject }} → {{ run.input }}</template>
        </span>
      </div>
      <div class="flex items-center gap-2">
        <Badge :variant="result.ok ? 'success' : 'danger'">
          {{ result.ok ? 'Link OK' : 'Failed' }}
        </Badge>
        <span class="font-mono text-xs text-fg-muted">
          {{ new Date(run.at).toLocaleTimeString() }}
        </span>
      </div>
    </header>

    <ol class="mt-4">
      <li
        v-for="(stage, i) in stages"
        :key="`${run.id}-${stage.key}`"
        class="relative flex gap-3 animate-stage-in"
        :class="i < stages.length - 1 ? 'pb-5' : ''"
        :style="{ animationDelay: `${i * 55}ms` }"
      >
        <div class="flex flex-col items-center self-stretch">
          <span
            class="flex h-6 w-6 shrink-0 items-center justify-center rounded-full border transition-shadow duration-200"
            :class="iconClasses[stage.state]"
            :aria-label="stage.state === 'ok' ? 'passed' : stage.state === 'fail' ? 'failed' : 'skipped'"
          >
            <Check v-if="stage.state === 'ok'" class="h-3.5 w-3.5" />
            <X v-else-if="stage.state === 'fail'" class="h-3.5 w-3.5" />
            <Minus v-else class="h-3.5 w-3.5" />
          </span>
          <span
            v-if="i < stages.length - 1"
            class="mt-1 w-px flex-1"
            :class="lineClasses[stage.state]"
            aria-hidden="true"
          />
        </div>

        <div class="min-w-0 flex-1 pt-0.5">
          <div class="flex flex-wrap items-center gap-2">
            <p
              class="text-sm font-medium"
              :class="stage.state === 'skip' ? 'text-fg-muted' : 'text-fg'"
            >
              {{ stage.label }}
            </p>
            <Badge v-if="stage.status !== undefined" :variant="statusTone(stage.status)" mono>
              {{ stage.status }}
            </Badge>
          </div>
          <p v-if="stage.value" class="mt-0.5 break-all font-mono text-xs text-fg-secondary">
            {{ stage.value }}
          </p>
          <p v-if="stage.note" class="mt-0.5 text-xs text-fg-muted">{{ stage.note }}</p>
          <p
            v-if="stage.error"
            class="mt-1.5 break-all rounded-md border border-danger/30 bg-danger-soft px-2.5 py-1.5 font-mono text-xs text-danger"
          >
            {{ stage.error }}
          </p>
          <p v-if="stage.hint" class="mt-1.5 flex items-start gap-1.5 text-xs text-fg-secondary">
            <Info class="mt-0.5 h-3.5 w-3.5 shrink-0 text-accent" />
            <span>{{ stage.hint }}</span>
          </p>
        </div>
      </li>
    </ol>
  </article>
</template>
