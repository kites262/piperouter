<!-- Shared staged diagnostics result (PRD §19.7, §16.1).
     Vertical stage list: Route Resolution → Target URL → Transport Selection →
     Connection → TLS (https only) → HTTP Response → Duration.
     error_stage marks the failed stage; stages after it render "−". -->
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
  /** Monospace value line (target URL, transport name, durations). */
  value?: string
  /** Secondary human-readable note. */
  note?: string
  /** Sanitized error message — shown on the failed stage only. */
  error?: string
  /** HTTP status rendered as a badge (HTTP Response stage). */
  status?: number
  /** Connectivity hint (§16.1: 401/403/404 still means the link works). */
  hint?: string
}

/**
 * Backend error stages mapped to UI stage groups, in pipeline order. The
 * backend's "resolve" stage means the probe request could not be BUILT
 * (bad route/transport/path/method) with no network activity, so it maps to
 * Route Resolution (-1), before any network stage — NOT to Connection.
 * connect → Connection (0), tls → TLS (1), response → HTTP Response (2).
 */
const FAIL_GROUPS: Record<string, number> = { resolve: -1, connect: 0, tls: 1, response: 2 }

const result = computed(() => props.run.result)

const targetUrl = computed(() => {
  if (result.value.target_url !== '') return result.value.target_url
  // Defensive fallback: a transport test always targets the entered URL.
  return props.run.kind === 'transport' ? props.run.input : ''
})

const isHttps = computed(() => targetUrl.value.toLowerCase().startsWith('https:'))

const stages = computed<Stage[]>(() => {
  const r = result.value
  const failGroup =
    r.error_stage === '' ? Number.POSITIVE_INFINITY : (FAIL_GROUPS[r.error_stage] ?? 0)
  const net = (group: number): StageState =>
    group < failGroup ? 'ok' : group === failGroup ? 'fail' : 'skip'

  const list: Stage[] = []

  // 1. Route Resolution — this is where a "resolve"-stage failure surfaces
  //    (the probe request could not be built: bad path/method/rewrite). A
  //    successful build means the route matched and was rewritten.
  if (props.run.kind === 'route') {
    const resolveFailed = r.error_stage === 'resolve'
    list.push({
      key: 'route',
      label: 'Route Resolution',
      state: resolveFailed ? 'fail' : 'ok',
      value: resolveFailed ? undefined : props.run.subject,
      note: resolveFailed ? undefined : 'route matched and rewritten through the real pipeline',
      error: resolveFailed ? r.error : undefined,
    })
  } else {
    list.push({
      key: 'route',
      label: 'Route Resolution',
      state: 'skip',
      note: 'not applicable for transport tests',
    })
  }

  // 2. Target URL
  list.push({
    key: 'target',
    label: 'Target URL',
    state: targetUrl.value !== '' ? 'ok' : 'skip',
    value: targetUrl.value !== '' ? targetUrl.value : undefined,
  })

  // 3. Transport Selection
  list.push({
    key: 'transport',
    label: 'Transport Selection',
    state: r.transport !== '' ? 'ok' : 'skip',
    value: r.transport !== '' ? r.transport : undefined,
  })

  // 4. Connection (DNS resolution + dial through the transport chain)
  const connState = net(0)
  list.push({
    key: 'connect',
    label: 'Connection',
    state: connState,
    note: connState === 'ok' ? 'connection established' : undefined,
    error: connState === 'fail' ? r.error : undefined,
  })

  // 5. TLS — only shown for https targets.
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

  // 6. HTTP Response
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

  // 7. Duration — informational; the total is measured even on failure.
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
  ok: 'border-success/40 bg-success-soft text-success',
  fail: 'border-danger/40 bg-danger-soft text-danger',
  skip: 'border-border bg-surface-raised text-fg-muted',
}
</script>

<template>
  <article class="card-flat p-4">
    <header class="flex flex-wrap items-center justify-between gap-3 border-b border-border pb-3">
      <div class="flex min-w-0 flex-wrap items-center gap-2">
        <Badge :variant="run.kind === 'route' ? 'accent' : 'default'">
          {{ run.kind === 'route' ? 'Route test' : 'Transport test' }}
        </Badge>
        <span class="font-mono text-xs text-fg-secondary">{{ run.method }}</span>
        <span class="min-w-0 max-w-full truncate font-mono text-xs text-fg" :title="run.input">
          <template v-if="run.kind === 'route'">{{ run.subject }} · {{ run.input }}</template>
          <template v-else>{{ run.input }}</template>
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
        :key="stage.key"
        class="relative flex gap-3"
        :class="i < stages.length - 1 ? 'pb-5' : ''"
      >
        <div class="flex flex-col items-center self-stretch">
          <span
            class="flex h-6 w-6 shrink-0 items-center justify-center rounded-full border"
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
            :class="stage.state === 'fail' ? 'bg-danger/40' : 'bg-border'"
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
