<!-- Route create/edit dialog (PRD §19.3) — live validation + mapping preview. -->
<script setup lang="ts">
import { computed, reactive, ref, watch } from 'vue'

import { ApiError, createRoute, updateRoute } from '@/api/client'
import type { RouteConfig, TransportConfig } from '@/api/types'
import Button from '@/components/ui/Button.vue'
import Dialog from '@/components/ui/Dialog.vue'
import Input from '@/components/ui/Input.vue'
import Select from '@/components/ui/Select.vue'
import Switch from '@/components/ui/Switch.vue'
import { useToast } from '@/composables/useToast'

import {
  normalizePrefix,
  previewMapping,
  validateName,
  validatePrefix,
  validateTarget,
  validateTransport,
} from './validation'

const open = defineModel<boolean>('open', { required: true })

const props = defineProps<{
  mode: 'create' | 'edit'
  /** Route being edited; ignored in create mode. */
  route?: RouteConfig | null
  /** Current routes — for name/prefix uniqueness checks. */
  routes: RouteConfig[]
  /** Available transports (includes built-in "direct"). */
  transports: TransportConfig[]
  /** Current config revision (optimistic concurrency, 409 on mismatch). */
  revision: string
}>()

const emit = defineEmits<{ saved: []; reload: [] }>()

const toast = useToast()

type FieldName = 'name' | 'prefix' | 'target' | 'transport'

const form = reactive({
  name: '',
  enabled: true,
  prefix: '',
  target: '',
  stripPrefix: true,
  stripForwardHeaders: true,
  transport: 'direct',
})

const touched = reactive<Record<FieldName, boolean>>({
  name: false,
  prefix: false,
  target: false,
  transport: false,
})

const submitted = ref(false)
const saving = ref(false)
const serverIssues = ref<string[]>([])
const serverError = ref('')
const conflict = ref(false)

const isEdit = computed(() => props.mode === 'edit')
const originalName = computed(() => (isEdit.value && props.route ? props.route.name : null))

watch(open, (isOpen) => {
  if (!isOpen) return
  const source = isEdit.value ? (props.route ?? null) : null
  form.name = source?.name ?? ''
  form.enabled = source?.enabled ?? true
  form.prefix = source?.prefix ?? ''
  form.target = source?.target ?? ''
  form.stripPrefix = source?.strip_prefix ?? true
  form.stripForwardHeaders = source?.strip_forward_headers ?? true
  form.transport = source?.transport ?? 'direct'
  touched.name = false
  touched.prefix = false
  touched.target = false
  touched.transport = false
  submitted.value = false
  saving.value = false
  serverIssues.value = []
  serverError.value = ''
  conflict.value = false
})

/** Always offer the built-in direct transport, even if the list failed to load. */
const transportOptions = computed<TransportConfig[]>(() => {
  if (props.transports.some((t) => t.name === 'direct')) return props.transports
  return [{ name: 'direct', type: 'direct', url: '' }, ...props.transports]
})

const fieldErrors = computed<Record<FieldName, string | null>>(() => ({
  name: validateName(form.name.trim(), props.routes, originalName.value),
  // Validate the raw value so a trailing "/" is flagged live; blur and
  // submit both auto-trim it (§6.4), so it never blocks saving.
  prefix: validatePrefix(form.prefix.trim(), props.routes, originalName.value),
  target: validateTarget(form.target.trim()),
  transport: validateTransport(form.transport, transportOptions.value),
}))

const hasErrors = computed(() => Object.values(fieldErrors.value).some((e) => e !== null))

/**
 * Live error visibility: show as soon as the field has content (validate while
 * typing), after it was blurred, or after a submit attempt.
 */
const visibleErrors = computed<Record<FieldName, string | null>>(() => {
  const values: Record<FieldName, string> = {
    name: form.name,
    prefix: form.prefix,
    target: form.target,
    transport: form.transport,
  }
  const out = {} as Record<FieldName, string | null>
  for (const field of ['name', 'prefix', 'target', 'transport'] as const) {
    out[field] =
      touched[field] || submitted.value || values[field] !== '' ? fieldErrors.value[field] : null
  }
  return out
})

/** §6.4 — auto-trim the non-root trailing "/" when the prefix field blurs. */
function onPrefixBlur(): void {
  touched.prefix = true
  const trimmed = normalizePrefix(form.prefix)
  if (trimmed !== form.prefix) form.prefix = trimmed
}

const preview = computed(() => previewMapping(form.prefix, form.target, form.stripPrefix))

function onReloadClick(): void {
  conflict.value = false
  emit('reload')
}

async function submit(): Promise<void> {
  submitted.value = true
  serverIssues.value = []
  serverError.value = ''
  conflict.value = false
  // §6.4 — normalize before validating so an un-blurred trailing "/" is
  // auto-trimmed rather than blocking the save.
  form.name = form.name.trim()
  form.target = form.target.trim()
  form.prefix = normalizePrefix(form.prefix)
  if (hasErrors.value || saving.value) return

  const payload: RouteConfig = {
    name: form.name.trim(),
    enabled: form.enabled,
    prefix: normalizePrefix(form.prefix),
    target: form.target.trim(),
    strip_forward_headers: form.stripForwardHeaders,
    strip_prefix: form.stripPrefix,
    transport: form.transport,
  }

  saving.value = true
  try {
    if (isEdit.value && originalName.value !== null) {
      await updateRoute(originalName.value, payload, props.revision)
      toast.success(`Route "${payload.name}" updated`)
    } else {
      await createRoute(payload, props.revision)
      toast.success(`Route "${payload.name}" created`)
    }
    emit('saved')
    open.value = false
  } catch (err) {
    if (err instanceof ApiError && err.code === 'revision_conflict') {
      conflict.value = true
      toast.warning('Configuration changed', {
        message: 'The config was modified elsewhere. Reload it and save again.',
      })
    } else if (err instanceof ApiError && err.code === 'validation_failed') {
      serverIssues.value =
        err.issues.length > 0 ? err.issues : [err.detail !== '' ? err.detail : 'Validation failed.']
    } else {
      serverError.value =
        err instanceof ApiError ? (err.detail !== '' ? err.detail : err.code) : String(err)
      toast.error('Failed to save route', { message: serverError.value })
    }
  } finally {
    saving.value = false
  }
}
</script>

<template>
  <Dialog
    v-model:open="open"
    :title="isEdit ? 'Edit Route' : 'New Route'"
    :description="
      isEdit
        ? `Changes to “${route?.name ?? ''}” apply immediately on save.`
        : 'Map a path prefix to an upstream target.'
    "
    max-width="max-w-xl"
  >
    <form class="space-y-4" novalidate @submit.prevent="submit">
      <!-- Name + Transport -->
      <div class="grid gap-4 sm:grid-cols-2">
        <label class="block space-y-1.5" @focusout="touched.name = true">
          <span class="text-xs font-medium text-fg-secondary">Name</span>
          <Input
            v-model="form.name"
            placeholder="openai"
            mono
            :disabled="isEdit"
            :invalid="visibleErrors.name !== null"
          />
          <span v-if="visibleErrors.name" class="block text-xs text-danger">
            {{ visibleErrors.name }}
          </span>
          <span v-else-if="isEdit" class="block text-xs text-fg-muted">
            Route names cannot be changed.
          </span>
        </label>

        <label class="block space-y-1.5" @focusout="touched.transport = true">
          <span class="text-xs font-medium text-fg-secondary">Transport</span>
          <Select v-model="form.transport" :invalid="visibleErrors.transport !== null">
            <option v-for="t in transportOptions" :key="t.name" :value="t.name">
              {{ t.name }} ({{ t.type === 'direct' ? 'built-in' : t.type }})
            </option>
          </Select>
          <span v-if="visibleErrors.transport" class="block text-xs text-danger">
            {{ visibleErrors.transport }}
          </span>
        </label>
      </div>

      <!-- Prefix -->
      <label class="block space-y-1.5" @focusout="onPrefixBlur">
        <span class="text-xs font-medium text-fg-secondary">Prefix</span>
        <Input
          v-model="form.prefix"
          placeholder="/openai"
          mono
          :invalid="visibleErrors.prefix !== null"
        />
        <span v-if="visibleErrors.prefix" class="block text-xs text-danger">
          {{ visibleErrors.prefix }}
        </span>
        <span v-else class="block text-xs text-fg-muted">
          Incoming paths matching this prefix are forwarded. Longest prefix wins.
        </span>
      </label>

      <!-- Target -->
      <label class="block space-y-1.5" @focusout="touched.target = true">
        <span class="text-xs font-medium text-fg-secondary">Target</span>
        <Input
          v-model="form.target"
          placeholder="https://api.openai.com/v1"
          mono
          :invalid="visibleErrors.target !== null"
        />
        <span v-if="visibleErrors.target" class="block text-xs text-danger">
          {{ visibleErrors.target }}
        </span>
        <span v-else class="block text-xs text-fg-muted">
          Absolute http(s) URL — no query, fragment or credentials.
        </span>
      </label>

      <!-- Switches -->
      <div class="grid gap-3 sm:grid-cols-2">
        <div class="flex items-center justify-between gap-3 rounded-md border border-border bg-bg-deep/40 px-3 py-2.5">
          <div class="min-w-0">
            <p class="text-sm text-fg">Enabled</p>
            <p class="text-xs text-fg-muted">Disabled routes never match.</p>
          </div>
          <Switch v-model="form.enabled" />
        </div>
        <div class="flex items-center justify-between gap-3 rounded-md border border-border bg-bg-deep/40 px-3 py-2.5">
          <div class="min-w-0">
            <p class="text-sm text-fg">Strip Prefix</p>
            <p class="text-xs text-fg-muted">Remove the prefix before forwarding.</p>
          </div>
          <Switch v-model="form.stripPrefix" />
        </div>
        <div
          class="flex items-center justify-between gap-3 rounded-md border border-border bg-bg-deep/40 px-3 py-2.5 sm:col-span-2"
        >
          <div class="min-w-0">
            <p class="text-sm text-fg">Strip Forward Headers</p>
            <p class="text-xs text-fg-muted">
              Hide client and proxy metadata (Forwarded, Via, X-Forwarded-*) from the target.
            </p>
          </div>
          <Switch v-model="form.stripForwardHeaders" />
        </div>
      </div>

      <!-- Mapping preview (PRD §19.3) -->
      <div class="rounded-md border border-border bg-bg-deep/70 p-3">
        <p class="text-[10px] font-medium uppercase tracking-widest text-fg-muted">
          Mapping preview
        </p>
        <template v-if="preview">
          <p class="mt-2 break-all font-mono text-xs text-fg">{{ preview.from }}</p>
          <p class="mt-1 break-all font-mono text-xs text-accent">→ {{ preview.to }}</p>
        </template>
        <p v-else class="mt-2 text-xs text-fg-muted">
          Enter a valid prefix and target to preview the request mapping.
        </p>
      </div>

      <!-- Server-side validation issues (400 validation_failed) -->
      <div
        v-if="serverIssues.length > 0"
        class="rounded-md border border-danger/30 bg-danger-soft p-3"
      >
        <p class="text-xs font-medium text-danger">The server rejected the configuration:</p>
        <ul class="mt-1.5 list-disc space-y-1 pl-4 text-xs text-danger">
          <li v-for="issue in serverIssues" :key="issue">{{ issue }}</li>
        </ul>
      </div>

      <!-- Revision conflict (409) -->
      <div
        v-if="conflict"
        class="flex items-center justify-between gap-3 rounded-md border border-warning/30 bg-warning-soft p-3"
      >
        <p class="text-xs text-warning">
          The configuration changed while you were editing. Reload the latest config, then save
          again.
        </p>
        <Button size="sm" variant="outline" @click="onReloadClick">Reload</Button>
      </div>

      <!-- Other server errors -->
      <div v-if="serverError" class="rounded-md border border-danger/30 bg-danger-soft p-3">
        <p class="text-xs text-danger">{{ serverError }}</p>
      </div>
    </form>

    <template #footer>
      <Button variant="ghost" :disabled="saving" @click="open = false">Cancel</Button>
      <Button :loading="saving" @click="submit">
        {{ isEdit ? 'Save Changes' : 'Create Route' }}
      </Button>
    </template>
  </Dialog>
</template>
