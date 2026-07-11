<!-- Diagnostics (PRD §19.7, §16): route + transport tests through the real
     proxy pipeline. Results render as a staged vertical checklist; past runs
     of this session stay in a small history list (never persisted). -->
<script setup lang="ts">
import { Activity, Play, RotateCw, Trash2, TriangleAlert } from 'lucide-vue-next'
import { computed, ref, watch } from 'vue'

import { ApiError, listRoutes, listTransports, testRoute, testTransport } from '@/api/client'
import type { RouteConfig, TransportConfig } from '@/api/types'
import { statusTone, useRunHistory } from '@/components/diagnostics/runs'
import StagedResult from '@/components/diagnostics/StagedResult.vue'
import Badge from '@/components/ui/Badge.vue'
import Button from '@/components/ui/Button.vue'
import Card from '@/components/ui/Card.vue'
import EmptyState from '@/components/ui/EmptyState.vue'
import Input from '@/components/ui/Input.vue'
import Select from '@/components/ui/Select.vue'
import StatusDot from '@/components/ui/StatusDot.vue'
import Tabs from '@/components/ui/Tabs.vue'
import { usePolling } from '@/composables/usePolling'
import { useToast } from '@/composables/useToast'

const toast = useToast()
const { runs, add: addRun, clear: clearRuns } = useRunHistory()

// The diagnostics API only accepts GET/HEAD/POST (diagnostics.AllowedMethod);
// offering others would guarantee a 400 and never run a test.
const METHODS = ['GET', 'HEAD', 'POST'] as const

const TABS = [
  { value: 'route', label: 'Route Test' },
  { value: 'transport', label: 'Transport Test' },
]
const tab = ref('route')

// --- selectable options (routes + transports) ------------------------------
const routes = ref<RouteConfig[] | null>(null)
const transports = ref<TransportConfig[] | null>(null)
const optionsError = ref<string | null>(null)

async function fetchOptions(): Promise<void> {
  try {
    const [r, t] = await Promise.all([listRoutes(), listTransports()])
    routes.value = r
    transports.value = t
    optionsError.value = null
  } catch (err) {
    const message =
      err instanceof ApiError ? (err.detail !== '' ? err.detail : err.code) : String(err)
    // Toast only on the ok → failed transition, never on every poll tick.
    if (optionsError.value === null) {
      toast.error('Failed to load routes and transports', { message })
    }
    optionsError.value = message
  }
}

// Low-frequency refresh keeps the selects in sync with config hot-reloads.
const { refresh: refreshOptions } = usePolling(fetchOptions, { interval: 15000 })

const optionsReady = computed(() => routes.value !== null && transports.value !== null)
const optionsLoading = computed(() => !optionsReady.value && optionsError.value === null)

/** Diagnostics run against the active snapshot — disabled routes 404 there. */
const enabledRoutes = computed<RouteConfig[]>(() =>
  (routes.value ?? []).filter((r) => r.enabled),
)

// --- forms ------------------------------------------------------------------
const routeName = ref('')
const routePath = ref('/')
const routeMethod = ref('GET')

const transportName = ref('direct')
const testUrl = ref('')
const transportMethod = ref('GET')
const urlTouched = ref(false)

// Keep selections valid as option lists refresh.
watch(enabledRoutes, (list) => {
  if (list.length === 0) {
    routeName.value = ''
  } else if (!list.some((r) => r.name === routeName.value)) {
    routeName.value = list[0]!.name
  }
})

watch(transports, (list) => {
  if (list !== null && list.length > 0 && !list.some((t) => t.name === transportName.value)) {
    transportName.value = 'direct'
  }
})

/** Validation message for the transport-test URL (must be absolute http/https). */
const urlError = computed<string | null>(() => {
  const raw = testUrl.value.trim()
  if (raw === '') return 'Test URL is required.'
  let parsed: URL
  try {
    parsed = new URL(raw)
  } catch {
    return 'Enter an absolute URL, e.g. https://example.com/health'
  }
  if (parsed.protocol !== 'http:' && parsed.protocol !== 'https:') {
    return 'The URL must use http:// or https://.'
  }
  return null
})

const showUrlError = computed(() => urlTouched.value && urlError.value !== null)

// --- run execution ----------------------------------------------------------
const running = ref(false)
const runError = ref<string | null>(null)
const selectedRunId = ref<number | null>(null)

// Stale form errors do not apply to the other test kind.
watch(tab, () => {
  runError.value = null
})

const displayedRun = computed(
  () => runs.find((r) => r.id === selectedRunId.value) ?? runs[0] ?? null,
)

function normalizePath(raw: string): string {
  const p = raw.trim()
  if (p === '') return '/'
  return p.startsWith('/') ? p : `/${p}`
}

function describeError(err: unknown): string {
  return err instanceof ApiError ? (err.detail !== '' ? err.detail : err.code) : String(err)
}

async function runRouteTest(): Promise<void> {
  if (running.value || routeName.value === '') return
  const path = normalizePath(routePath.value)
  routePath.value = path
  running.value = true
  runError.value = null
  try {
    const result = await testRoute({
      route: routeName.value,
      path,
      method: routeMethod.value,
    })
    selectedRunId.value = addRun({
      kind: 'route',
      subject: routeName.value,
      input: path,
      method: routeMethod.value,
      result,
    }).id
  } catch (err) {
    const message = describeError(err)
    runError.value = message
    toast.error('Route test failed', { message })
  } finally {
    running.value = false
  }
}

async function runTransportTest(): Promise<void> {
  if (running.value) return
  urlTouched.value = true
  if (urlError.value !== null) return
  const url = testUrl.value.trim()
  testUrl.value = url
  running.value = true
  runError.value = null
  try {
    const result = await testTransport({
      transport: transportName.value,
      url,
      method: transportMethod.value,
    })
    selectedRunId.value = addRun({
      kind: 'transport',
      subject: transportName.value,
      input: url,
      method: transportMethod.value,
      result,
    }).id
  } catch (err) {
    const message = describeError(err)
    runError.value = message
    toast.error('Transport test failed', { message })
  } finally {
    running.value = false
  }
}

function submit(): void {
  if (tab.value === 'route') void runRouteTest()
  else void runTransportTest()
}

function clearHistory(): void {
  clearRuns()
  selectedRunId.value = null
}
</script>

<template>
  <section class="space-y-6">
    <header>
      <h1 class="text-lg font-semibold text-fg">Diagnostics</h1>
      <p class="mt-1 text-sm text-fg-muted">
        Route and transport tests through the real proxy pipeline — rewrite, transport, target.
      </p>
    </header>

    <Tabs v-model="tab" :tabs="TABS" />

    <!-- Options poll failing but stale lists still usable -->
    <div
      v-if="optionsError !== null && optionsReady"
      class="flex items-center gap-2 rounded-lg border border-warning/30 bg-warning-soft px-3 py-2 text-xs text-warning"
      role="alert"
    >
      <TriangleAlert class="h-3.5 w-3.5 shrink-0" />
      <span class="min-w-0 truncate">
        Route/transport lists unavailable ({{ optionsError }}) — retrying. Showing last known lists.
      </span>
    </div>

    <div class="grid items-start gap-6 xl:grid-cols-5">
      <!-- Test form -->
      <div class="xl:col-span-2">
        <Card :title="tab === 'route' ? 'Route Test' : 'Transport Test'">
          <!-- Loading skeleton -->
          <div v-if="optionsLoading" class="space-y-4" aria-hidden="true">
            <div v-for="i in 3" :key="i">
              <div class="h-3 w-16 animate-pulse rounded bg-surface-raised" />
              <div class="mt-2 h-9 w-full animate-pulse rounded-md bg-surface-raised" />
            </div>
            <div class="h-9 w-28 animate-pulse rounded-md bg-surface-raised" />
          </div>

          <!-- Options never loaded -->
          <div v-else-if="!optionsReady" class="space-y-3">
            <p class="flex items-start gap-2 text-sm text-danger">
              <TriangleAlert class="mt-0.5 h-4 w-4 shrink-0" />
              <span class="min-w-0 break-words">{{ optionsError }}</span>
            </p>
            <Button variant="outline" size="sm" @click="refreshOptions">
              <RotateCw class="h-3.5 w-3.5" />
              Retry
            </Button>
          </div>

          <form v-else class="space-y-4" @submit.prevent="submit">
            <!-- Route test fields -->
            <template v-if="tab === 'route'">
              <label class="block">
                <span class="mb-1.5 block text-xs font-medium text-fg-secondary">Route</span>
                <Select v-model="routeName" :disabled="enabledRoutes.length === 0">
                  <option v-for="r in enabledRoutes" :key="r.name" :value="r.name">
                    {{ r.name }} — {{ r.prefix }}
                  </option>
                </Select>
                <span v-if="enabledRoutes.length === 0" class="mt-1.5 block text-xs text-warning">
                  No enabled routes to test. Enable or create a route first.
                </span>
              </label>

              <label class="block">
                <span class="mb-1.5 block text-xs font-medium text-fg-secondary">Path</span>
                <Input v-model="routePath" mono placeholder="/" />
                <span class="mt-1.5 block text-xs text-fg-muted">
                  Request path sent to the route, e.g. <span class="font-mono">/models</span>.
                </span>
              </label>
            </template>

            <!-- Transport test fields -->
            <template v-else>
              <label class="block">
                <span class="mb-1.5 block text-xs font-medium text-fg-secondary">Transport</span>
                <Select v-model="transportName">
                  <option v-for="t in transports ?? []" :key="t.name" :value="t.name">
                    {{ t.name }} ({{ t.type }})
                  </option>
                </Select>
              </label>

              <label class="block">
                <span class="mb-1.5 block text-xs font-medium text-fg-secondary">URL</span>
                <Input
                  v-model="testUrl"
                  mono
                  placeholder="https://example.com/health"
                  :invalid="showUrlError"
                  @blur="urlTouched = true"
                />
                <span v-if="showUrlError" class="mt-1.5 block text-xs text-danger">
                  {{ urlError }}
                </span>
                <span v-else class="mt-1.5 block text-xs text-fg-muted">
                  Absolute http/https URL. The test URL is never saved.
                </span>
              </label>
            </template>

            <label class="block">
              <span class="mb-1.5 block text-xs font-medium text-fg-secondary">Method</span>
              <Select v-if="tab === 'route'" v-model="routeMethod">
                <option v-for="m in METHODS" :key="m" :value="m">{{ m }}</option>
              </Select>
              <Select v-else v-model="transportMethod">
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

            <Button
              type="submit"
              :loading="running"
              :disabled="tab === 'route' && enabledRoutes.length === 0"
            >
              <Play v-if="!running" class="h-3.5 w-3.5" />
              Run test
            </Button>
          </form>
        </Card>
      </div>

      <!-- Result + session history -->
      <div class="space-y-6 xl:col-span-3">
        <StagedResult v-if="displayedRun !== null" :run="displayedRun" />
        <EmptyState
          v-else
          title="No tests run yet"
          description="Pick a route or transport on the left and run a test. Results show every pipeline stage: resolution, connection, TLS and the HTTP response."
        >
          <template #icon><Activity class="h-5 w-5" /></template>
        </EmptyState>

        <Card v-if="runs.length > 0" title="Session history">
          <template #header>
            <Button variant="ghost" size="sm" @click="clearHistory">
              <Trash2 class="h-3.5 w-3.5" />
              Clear
            </Button>
          </template>
          <ul class="space-y-1">
            <li v-for="run in runs" :key="run.id">
              <button
                type="button"
                class="flex w-full items-center gap-2.5 rounded-md px-2.5 py-1.5 text-left text-xs transition-colors duration-150"
                :class="
                  displayedRun !== null && displayedRun.id === run.id
                    ? 'bg-accent-soft'
                    : 'hover:bg-surface-raised'
                "
                @click="selectedRunId = run.id"
              >
                <StatusDot :status="run.result.ok ? 'success' : 'danger'" :pulse="false" />
                <span class="w-14 shrink-0 text-fg-muted">
                  {{ run.kind === 'route' ? 'route' : 'transport' }}
                </span>
                <span class="w-12 shrink-0 font-mono text-fg-secondary">{{ run.method }}</span>
                <span class="min-w-0 flex-1 truncate font-mono text-fg" :title="run.input">
                  <template v-if="run.kind === 'route'">{{ run.subject }} · {{ run.input }}</template>
                  <template v-else>{{ run.subject }} → {{ run.input }}</template>
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
