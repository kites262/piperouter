<!-- Diagnostics: simulate an inbound data-plane request.
     Path is matched against the live route table (longest-prefix), then the
     real rewrite → transport → target pipeline runs. Per-route and per-
     transport connectivity tests live on the Routes / Transports pages. -->
<script setup lang="ts">
import { Activity, Play, Trash2 } from 'lucide-vue-next'
import { computed, ref } from 'vue'

import { ApiError, testRequest } from '@/api/client'
import { statusTone, useRunHistory } from '@/components/diagnostics/runs'
import StagedResult from '@/components/diagnostics/StagedResult.vue'
import Badge from '@/components/ui/Badge.vue'
import Button from '@/components/ui/Button.vue'
import Card from '@/components/ui/Card.vue'
import EmptyState from '@/components/ui/EmptyState.vue'
import Input from '@/components/ui/Input.vue'
import Select from '@/components/ui/Select.vue'
import StatusDot from '@/components/ui/StatusDot.vue'
import { useToast } from '@/composables/useToast'

const toast = useToast()
const { runs, add: addRun, clear: clearRuns } = useRunHistory()

// Diagnostics API only accepts GET/HEAD/POST (diagnostics.AllowedMethod).
const METHODS = ['GET', 'HEAD', 'POST'] as const

const path = ref('/')
const method = ref('GET')
const pathTouched = ref(false)
const running = ref(false)
const runError = ref<string | null>(null)
const selectedRunId = ref<number | null>(null)

const pathError = computed<string | null>(() => {
  const raw = path.value.trim()
  if (raw === '') return null // empty is treated as "/"
  if (!raw.startsWith('/')) return 'Path must start with /.'
  return null
})

const showPathError = computed(() => pathTouched.value && pathError.value !== null)

/** Only inbound-path probes; older route/transport runs stay out of this page. */
const requestRuns = computed(() => runs.filter((r) => r.kind === 'request'))

const displayedRun = computed(
  () =>
    requestRuns.value.find((r) => r.id === selectedRunId.value) ?? requestRuns.value[0] ?? null,
)

function normalizePath(raw: string): string {
  const p = raw.trim()
  if (p === '') return '/'
  return p.startsWith('/') ? p : `/${p}`
}

function describeError(err: unknown): string {
  return err instanceof ApiError ? (err.detail !== '' ? err.detail : err.code) : String(err)
}

async function submit(): Promise<void> {
  pathTouched.value = true
  if (running.value || pathError.value !== null) return

  const reqPath = normalizePath(path.value)
  path.value = reqPath
  running.value = true
  runError.value = null
  try {
    const result = await testRequest({ path: reqPath, method: method.value })
    selectedRunId.value = addRun({
      kind: 'request',
      subject: result.route,
      input: reqPath,
      method: method.value,
      result,
    }).id
  } catch (err) {
    const message = describeError(err)
    runError.value = message
    toast.error('Request probe failed', { message })
  } finally {
    running.value = false
  }
}

function clearHistory(): void {
  clearRuns()
  selectedRunId.value = null
}
</script>

<template>
  <section class="space-y-6">
    <header class="animate-fade-up">
      <h1 class="text-lg font-semibold tracking-tight text-fg">Diagnostics</h1>
      <p class="mt-1 text-sm text-fg-muted">
        Probe a request as if it hit the proxy: path is matched with the live route table, then
        rewritten and forwarded. To test a specific route or transport, use the Test action on
        those pages.
      </p>
    </header>

    <div class="grid items-start gap-6 xl:grid-cols-5">
      <div class="animate-fade-up stagger-1 xl:col-span-2">
        <Card glass title="Request probe">
          <form class="space-y-4" @submit.prevent="submit">
            <label class="block">
              <span class="mb-1.5 block text-xs font-medium text-fg-secondary">Path</span>
              <Input
                v-model="path"
                mono
                placeholder="/openai/models"
                :invalid="showPathError"
                @blur="pathTouched = true"
              />
              <span v-if="showPathError" class="mt-1.5 block text-xs text-danger">
                {{ pathError }}
              </span>
              <span v-else class="mt-1.5 block text-xs text-fg-muted">
                Full path a client would send, e.g.
                <span class="font-mono">/openai/models</span>. Matched with longest-prefix rules.
              </span>
            </label>

            <label class="block">
              <span class="mb-1.5 block text-xs font-medium text-fg-secondary">Method</span>
              <Select v-model="method">
                <option v-for="m in METHODS" :key="m" :value="m">{{ m }}</option>
              </Select>
            </label>

            <p
              v-if="runError !== null"
              class="break-words rounded-md border border-danger/30 bg-danger-soft px-3 py-2 text-xs text-danger"
              role="alert"
            >
              {{ runError }}
            </p>

            <Button type="submit" :loading="running">
              <Play v-if="!running" class="h-3.5 w-3.5" />
              Run probe
            </Button>
          </form>
        </Card>
      </div>

      <div class="animate-fade-up stagger-2 space-y-6 xl:col-span-3">
        <StagedResult v-if="displayedRun !== null" :run="displayedRun" />
        <EmptyState
          v-else
          title="No probes yet"
          description="Enter a request path on the left. Results show route match, rewrite target, transport, connection, TLS and the HTTP response."
        >
          <template #icon><Activity class="h-5 w-5" /></template>
        </EmptyState>

        <Card v-if="requestRuns.length > 0" glass title="Session history">
          <template #header>
            <Button variant="ghost" size="sm" @click="clearHistory">
              <Trash2 class="h-3.5 w-3.5" />
              Clear
            </Button>
          </template>
          <ul class="space-y-1">
            <li v-for="run in requestRuns" :key="run.id">
              <button
                type="button"
                class="flex w-full items-center gap-2.5 rounded-lg px-2.5 py-1.5 text-left text-xs transition-colors duration-150"
                :class="
                  displayedRun !== null && displayedRun.id === run.id
                    ? 'bg-accent-soft shadow-[inset_0_0_0_1px_color-mix(in_srgb,var(--color-accent)_22%,transparent)]'
                    : 'hover:bg-surface-raised/70'
                "
                @click="selectedRunId = run.id"
              >
                <StatusDot :status="run.result.ok ? 'success' : 'danger'" :pulse="false" />
                <span class="w-12 shrink-0 font-mono text-fg-secondary">{{ run.method }}</span>
                <span class="min-w-0 flex-1 truncate font-mono text-fg" :title="run.input">
                  {{ run.input }}
                  <template v-if="run.result.route !== ''">
                    <span class="text-fg-muted"> · {{ run.result.route }}</span>
                  </template>
                </span>
                <Badge
                  v-if="run.result.status > 0"
                  :variant="statusTone(run.result.status)"
                  mono
                >
                  {{ run.result.status }}
                </Badge>
                <Badge v-else variant="danger" mono>ERR</Badge>
                <span class="shrink-0 font-mono text-fg-muted">
                  {{ new Date(run.at).toLocaleTimeString() }}
                </span>
              </button>
            </li>
          </ul>
        </Card>
      </div>
    </div>
  </section>
</template>
