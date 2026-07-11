<!-- Settings (PRD §19.8): edit the limited global configuration — proxy/admin
     listeners, TLS, log level and recent-log capacity — plus a read-only
     About card. Listener/TLS changes require a restart; log_level and
     recent_logs apply hot. Saves go through PUT /api/v1/config with the
     loaded revision (409 → reload prompt, validation issues inline). -->
<script setup lang="ts">
import { Info, RotateCw, Save, TriangleAlert, Undo2 } from 'lucide-vue-next'
import { computed, inject, reactive, ref } from 'vue'

import { ApiError, getConfig, putConfig } from '@/api/client'
import type { Config, ConfigEnvelope, LogLevel } from '@/api/types'
import Badge from '@/components/ui/Badge.vue'
import Button from '@/components/ui/Button.vue'
import Card from '@/components/ui/Card.vue'
import Input from '@/components/ui/Input.vue'
import Select from '@/components/ui/Select.vue'
import Switch from '@/components/ui/Switch.vue'
import { statusKey } from '@/composables/useStatus'
import { useToast } from '@/composables/useToast'

const toast = useToast()

// Shared 5s status poll from AppShell — feeds the read-only About card.
const statusState = inject(statusKey, null)
const status = computed(() => statusState?.status.value ?? null)

const LOG_LEVELS: LogLevel[] = ['debug', 'info', 'warn', 'error']

// --- config load ------------------------------------------------------------
const envelope = ref<ConfigEnvelope | null>(null)
const loadError = ref<string | null>(null)
const loading = ref(false)

interface FormState {
  proxyListen: string
  tlsEnabled: boolean
  tlsCert: string
  tlsKey: string
  adminListen: string
  logLevel: string
  recentLogs: string
}

const form = reactive<FormState>({
  proxyListen: '',
  tlsEnabled: false,
  tlsCert: '',
  tlsKey: '',
  adminListen: '',
  logLevel: 'info',
  recentLogs: '1000',
})

/** Snapshot of the form as loaded from the server — dirty-state baseline. */
const baseline = ref<FormState | null>(null)

function toForm(c: Config): FormState {
  return {
    proxyListen: c.server.proxy.listen,
    tlsEnabled: c.server.proxy.tls.enabled,
    tlsCert: c.server.proxy.tls.cert_file,
    tlsKey: c.server.proxy.tls.key_file,
    adminListen: c.server.admin.listen,
    logLevel: c.runtime.log_level,
    recentLogs: String(c.runtime.recent_logs),
  }
}

function resetForm(c: Config): void {
  const next = toForm(c)
  Object.assign(form, next)
  baseline.value = { ...next }
  submitted.value = false
}

async function loadConfig(): Promise<void> {
  loading.value = true
  try {
    const env = await getConfig()
    envelope.value = env
    resetForm(env.config)
    loadError.value = null
    conflict.value = false
    serverIssues.value = []
  } catch (err) {
    const message =
      err instanceof ApiError ? (err.detail !== '' ? err.detail : err.code) : String(err)
    loadError.value = message
    if (envelope.value === null) {
      toast.error('Failed to load configuration', { message })
    }
  } finally {
    loading.value = false
  }
}

// One-shot load — never poll an editable form (edits must not be clobbered).
void loadConfig()

const initialLoading = computed(() => envelope.value === null && loadError.value === null)

// --- validation & dirty tracking ---------------------------------------------
const submitted = ref(false)

const fieldErrors = computed<Partial<Record<keyof FormState, string>>>(() => {
  const errors: Partial<Record<keyof FormState, string>> = {}
  if (form.proxyListen.trim() === '') errors.proxyListen = 'Proxy listen address is required.'
  if (form.adminListen.trim() === '') errors.adminListen = 'Admin listen address is required.'
  if (form.tlsEnabled) {
    if (form.tlsCert.trim() === '') {
      errors.tlsCert = 'A certificate file is required while TLS is enabled.'
    }
    if (form.tlsKey.trim() === '') {
      errors.tlsKey = 'A key file is required while TLS is enabled.'
    }
  }
  // Note: v-model on input[type=number] may hand back a number at runtime,
  // so never call string methods on recentLogs directly.
  const recentRaw = String(form.recentLogs).trim()
  const n = Number(recentRaw)
  if (recentRaw === '' || !Number.isInteger(n) || n < 0) {
    errors.recentLogs = 'Recent-log capacity must be an integer ≥ 0.'
  }
  return errors
})

const hasFieldErrors = computed(() => Object.keys(fieldErrors.value).length > 0)

function fieldError(key: keyof FormState): string | undefined {
  return submitted.value ? fieldErrors.value[key] : undefined
}

const isDirty = computed(() => {
  if (baseline.value === null) return false
  const b = baseline.value
  return (
    form.proxyListen !== b.proxyListen ||
    form.tlsEnabled !== b.tlsEnabled ||
    form.tlsCert !== b.tlsCert ||
    form.tlsKey !== b.tlsKey ||
    form.adminListen !== b.adminListen ||
    form.logLevel !== b.logLevel ||
    String(form.recentLogs) !== b.recentLogs
  )
})

/** True while a restart-requiring field (listen/TLS) differs from the baseline. */
const restartFieldsDirty = computed(() => {
  if (baseline.value === null) return false
  const b = baseline.value
  return (
    form.proxyListen !== b.proxyListen ||
    form.tlsEnabled !== b.tlsEnabled ||
    form.tlsCert !== b.tlsCert ||
    form.tlsKey !== b.tlsKey ||
    form.adminListen !== b.adminListen
  )
})

// --- save --------------------------------------------------------------------
const saving = ref(false)
const serverIssues = ref<string[]>([])
const conflict = ref(false)
/** Set after a successful save that touched listener/TLS fields. */
const restartPending = ref(false)

async function save(): Promise<void> {
  if (envelope.value === null || saving.value) return
  submitted.value = true
  if (hasFieldErrors.value) return

  // Clone the loaded (normalized) config and change ONLY the settings fields.
  const cfg: Config = JSON.parse(JSON.stringify(envelope.value.config)) as Config
  cfg.server.proxy.listen = form.proxyListen.trim()
  cfg.server.proxy.tls.enabled = form.tlsEnabled
  cfg.server.proxy.tls.cert_file = form.tlsCert.trim()
  cfg.server.proxy.tls.key_file = form.tlsKey.trim()
  cfg.server.admin.listen = form.adminListen.trim()
  cfg.runtime.log_level = form.logLevel as LogLevel
  cfg.runtime.recent_logs = Number(String(form.recentLogs).trim())

  const needsRestart = restartFieldsDirty.value
  saving.value = true
  serverIssues.value = []
  conflict.value = false
  try {
    await putConfig({ revision: envelope.value.revision, config: cfg })
    if (needsRestart) restartPending.value = true
    toast.success('Configuration saved', {
      message: needsRestart
        ? 'Restart PipeRouter for listener/TLS changes to take effect.'
        : 'Log level and recent-log capacity are applied immediately.',
    })
    await loadConfig() // fresh revision + normalized values
    void statusState?.refresh()
  } catch (err) {
    if (err instanceof ApiError && err.status === 409) {
      conflict.value = true
      toast.warning('Revision conflict', {
        message: 'The configuration changed elsewhere. Reload before saving.',
      })
    } else if (err instanceof ApiError && err.code === 'validation_failed') {
      serverIssues.value = err.issues.length > 0 ? err.issues : [err.detail || 'Validation failed.']
      toast.error('Validation failed', {
        message: `${serverIssues.value.length} issue${serverIssues.value.length === 1 ? '' : 's'} — see details below.`,
      })
    } else {
      const message =
        err instanceof ApiError ? (err.detail !== '' ? err.detail : err.code) : String(err)
      toast.error('Failed to save configuration', { message })
    }
  } finally {
    saving.value = false
  }
}

function discard(): void {
  if (envelope.value !== null) resetForm(envelope.value.config)
  serverIssues.value = []
  conflict.value = false
}

// --- About helpers -----------------------------------------------------------
function formatUptime(seconds: number): string {
  const s = Math.max(0, Math.floor(seconds))
  const d = Math.floor(s / 86400)
  const h = Math.floor((s % 86400) / 3600)
  const m = Math.floor((s % 3600) / 60)
  if (d > 0) return `${d}d ${h}h ${m}m`
  if (h > 0) return `${h}h ${m}m`
  if (m > 0) return `${m}m ${s % 60}s`
  return `${s}s`
}
</script>

<template>
  <section class="space-y-6">
    <header class="animate-fade-up">
      <h1 class="text-lg font-semibold tracking-tight text-fg">Settings</h1>
      <p class="mt-1 text-sm text-fg-muted">
        Listeners, TLS, log level and recent-log capacity. Routes and transports are managed on
        their own pages and always hot-reload.
      </p>
    </header>

    <div class="grid items-start gap-6 xl:grid-cols-3">
      <div class="animate-fade-up stagger-1 space-y-4 xl:col-span-2">
        <template v-if="initialLoading">
          <div v-for="i in 3" :key="i" class="glass-panel p-4" aria-hidden="true">
            <div class="h-4 w-32 animate-pulse rounded bg-surface-raised" />
            <div class="mt-4 space-y-4">
              <div class="h-9 w-full animate-pulse rounded-md bg-surface-raised" />
              <div class="h-9 w-2/3 animate-pulse rounded-md bg-surface-raised" />
            </div>
          </div>
        </template>

        <Card v-else-if="envelope === null" glass title="Configuration unavailable">
          <p class="flex items-start gap-2 text-sm text-danger">
            <TriangleAlert class="mt-0.5 h-4 w-4 shrink-0" />
            <span class="min-w-0 break-words">{{ loadError }}</span>
          </p>
          <Button class="mt-4" variant="outline" size="sm" :loading="loading" @click="loadConfig">
            <RotateCw class="h-3.5 w-3.5" />
            Retry
          </Button>
        </Card>

        <form v-else class="space-y-4" @submit.prevent="save">
          <!-- 409 revision conflict → reload prompt -->
          <div
            v-if="conflict"
            class="flex flex-wrap items-center gap-3 rounded-lg border border-warning/40 bg-warning-soft px-4 py-3"
            role="alert"
          >
            <TriangleAlert class="h-4 w-4 shrink-0 text-warning" />
            <div class="min-w-0 flex-1">
              <p class="text-sm font-medium text-warning">
                The configuration was changed by another client or externally.
              </p>
              <p class="mt-0.5 text-xs text-warning/80">
                Reload to get the latest revision — your unsaved edits here will be discarded.
              </p>
            </div>
            <Button variant="outline" size="sm" :loading="loading" @click="loadConfig">
              <RotateCw class="h-3.5 w-3.5" />
              Reload configuration
            </Button>
          </div>

          <!-- Saved, restart still pending -->
          <div
            v-if="restartPending"
            class="flex items-start gap-2.5 rounded-lg border border-warning/40 bg-warning-soft px-4 py-3"
            role="alert"
          >
            <TriangleAlert class="mt-0.5 h-4 w-4 shrink-0 text-warning" />
            <p class="text-sm text-warning">
              Saved. Listener/TLS changes take effect after PipeRouter is restarted.
            </p>
          </div>

          <!-- PROMINENT restart notice (§19.8) -->
          <div
            class="flex items-start gap-2.5 rounded-lg border border-warning/40 bg-warning-soft px-4 py-3"
            role="note"
          >
            <TriangleAlert class="mt-0.5 h-4 w-4 shrink-0 text-warning" />
            <p class="text-sm text-warning">
              Changing listen addresses or TLS requires a restart to take effect.
              <span class="text-warning/80">
                Log level and recent-log capacity apply immediately, without a restart.
              </span>
            </p>
          </div>

          <!-- Proxy server -->
          <Card glass title="Proxy server">
            <template #header>
              <Badge variant="warning">restart required</Badge>
            </template>
            <div class="space-y-4">
              <label class="block">
                <span class="mb-1.5 block text-xs font-medium text-fg-secondary">
                  Listen address
                </span>
                <Input
                  v-model="form.proxyListen"
                  mono
                  placeholder=":8080"
                  :invalid="fieldError('proxyListen') !== undefined"
                />
                <span v-if="fieldError('proxyListen')" class="mt-1.5 block text-xs text-danger">
                  {{ fieldError('proxyListen') }}
                </span>
                <span v-else class="mt-1.5 block text-xs text-fg-muted">
                  Data-plane listener, e.g. <span class="font-mono">:8080</span> or
                  <span class="font-mono">127.0.0.1:8080</span>.
                </span>
              </label>

              <div class="flex items-center justify-between gap-4 rounded-md border border-border px-3 py-2.5">
                <div>
                  <p class="text-sm text-fg">TLS</p>
                  <p class="mt-0.5 text-xs text-fg-muted">
                    Terminate HTTPS on the proxy listener with an existing certificate. No ACME.
                  </p>
                </div>
                <Switch v-model="form.tlsEnabled" />
              </div>

              <div v-if="form.tlsEnabled" class="grid gap-4 sm:grid-cols-2">
                <label class="block">
                  <span class="mb-1.5 block text-xs font-medium text-fg-secondary">
                    Certificate file
                  </span>
                  <Input
                    v-model="form.tlsCert"
                    mono
                    placeholder="/etc/piperouter/cert.pem"
                    :invalid="fieldError('tlsCert') !== undefined"
                  />
                  <span v-if="fieldError('tlsCert')" class="mt-1.5 block text-xs text-danger">
                    {{ fieldError('tlsCert') }}
                  </span>
                </label>
                <label class="block">
                  <span class="mb-1.5 block text-xs font-medium text-fg-secondary">Key file</span>
                  <Input
                    v-model="form.tlsKey"
                    mono
                    placeholder="/etc/piperouter/key.pem"
                    :invalid="fieldError('tlsKey') !== undefined"
                  />
                  <span v-if="fieldError('tlsKey')" class="mt-1.5 block text-xs text-danger">
                    {{ fieldError('tlsKey') }}
                  </span>
                </label>
              </div>
            </div>
          </Card>

          <!-- Admin server -->
          <Card glass title="Admin server">
            <template #header>
              <Badge variant="warning">restart required</Badge>
            </template>
            <label class="block">
              <span class="mb-1.5 block text-xs font-medium text-fg-secondary">Listen address</span>
              <Input
                v-model="form.adminListen"
                mono
                placeholder="127.0.0.1:9090"
                :invalid="fieldError('adminListen') !== undefined"
              />
              <span v-if="fieldError('adminListen')" class="mt-1.5 block text-xs text-danger">
                {{ fieldError('adminListen') }}
              </span>
              <span v-else class="mt-1.5 block text-xs text-fg-muted">
                Admin API + WebUI listener. Keep it on loopback — it has no authentication.
              </span>
            </label>
          </Card>

          <!-- Runtime -->
          <Card glass title="Runtime">
            <template #header>
              <Badge variant="success">applies hot</Badge>
            </template>
            <div class="grid gap-4 sm:grid-cols-2">
              <label class="block">
                <span class="mb-1.5 block text-xs font-medium text-fg-secondary">Log level</span>
                <Select v-model="form.logLevel">
                  <option v-for="level in LOG_LEVELS" :key="level" :value="level">
                    {{ level }}
                  </option>
                </Select>
              </label>
              <label class="block">
                <span class="mb-1.5 block text-xs font-medium text-fg-secondary">
                  Recent logs (ring buffer)
                </span>
                <Input
                  v-model="form.recentLogs"
                  mono
                  type="number"
                  min="0"
                  step="1"
                  :invalid="fieldError('recentLogs') !== undefined"
                />
                <span v-if="fieldError('recentLogs')" class="mt-1.5 block text-xs text-danger">
                  {{ fieldError('recentLogs') }}
                </span>
                <span v-else class="mt-1.5 block text-xs text-fg-muted">
                  In-memory access-log entries kept for the Logs page. 0 disables the buffer.
                </span>
              </label>
            </div>
          </Card>

          <!-- Server-side validation issues -->
          <div
            v-if="serverIssues.length > 0"
            class="rounded-lg border border-danger/40 bg-danger-soft px-4 py-3"
            role="alert"
          >
            <p class="text-sm font-medium text-danger">The server rejected the configuration:</p>
            <ul class="mt-1.5 list-disc space-y-1 pl-5">
              <li v-for="(issue, i) in serverIssues" :key="i" class="text-xs text-danger">
                {{ issue }}
              </li>
            </ul>
          </div>

          <!-- Poll-independent load error while stale form is on screen -->
          <p
            v-if="loadError !== null"
            class="break-words rounded-md border border-warning/30 bg-warning-soft px-3 py-2 text-xs text-warning"
            role="alert"
          >
            Reloading the configuration failed ({{ loadError }}). The form still shows the last
            loaded values.
          </p>

          <div class="flex flex-wrap items-center gap-3 pt-1">
            <Button type="submit" :loading="saving" :disabled="!isDirty || conflict">
              <Save v-if="!saving" class="h-3.5 w-3.5" />
              Save changes
            </Button>
            <Button v-if="isDirty" variant="outline" :disabled="saving" @click="discard">
              <Undo2 class="h-3.5 w-3.5" />
              Discard
            </Button>
            <span
              v-if="isDirty && restartFieldsDirty"
              class="flex items-center gap-1.5 text-xs text-warning"
            >
              <TriangleAlert class="h-3.5 w-3.5 shrink-0" />
              Includes listener/TLS changes — a restart will be required after saving.
            </span>
          </div>
        </form>
      </div>

      <!-- ============================== About ============================== -->
      <Card glass class="animate-fade-up stagger-2" title="About">
        <template #header>
          <Badge variant="muted">read-only</Badge>
        </template>
        <dl v-if="status !== null" class="space-y-3">
          <div>
            <dt class="text-xs text-fg-muted">Version</dt>
            <dd class="mt-0.5 font-mono text-sm text-fg">{{ status.version }}</dd>
          </div>
          <div>
            <dt class="text-xs text-fg-muted">Uptime</dt>
            <dd class="mt-0.5 font-mono text-sm text-fg">
              {{ formatUptime(status.uptime_seconds) }}
              <span class="text-xs text-fg-muted">
                · since {{ new Date(status.started_at).toLocaleString() }}
              </span>
            </dd>
          </div>
          <div>
            <dt class="text-xs text-fg-muted">Config file</dt>
            <dd class="mt-0.5 break-all font-mono text-sm text-fg">{{ status.config.path }}</dd>
          </div>
          <div>
            <dt class="text-xs text-fg-muted">Active revision</dt>
            <dd class="mt-0.5 break-all font-mono text-xs text-fg-secondary">
              {{ status.config.revision }}
            </dd>
            <dd class="mt-1 text-xs text-fg-muted">
              loaded {{ new Date(status.config.loaded_at).toLocaleString() }}
            </dd>
          </div>
        </dl>
        <div v-else class="space-y-3" aria-hidden="true">
          <div v-for="i in 4" :key="i">
            <div class="h-3 w-16 animate-pulse rounded bg-surface-raised" />
            <div class="mt-1.5 h-4 w-40 animate-pulse rounded bg-surface-raised" />
          </div>
        </div>
        <p class="mt-4 flex items-start gap-1.5 border-t border-border pt-3 text-xs text-fg-muted">
          <Info class="mt-0.5 h-3.5 w-3.5 shrink-0" />
          The YAML configuration file is the single source of truth. Saving here rewrites it
          atomically and keeps a .bak of the previous version.
        </p>
      </Card>
    </div>
  </section>
</template>
