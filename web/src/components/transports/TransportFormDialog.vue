<!-- Create/edit dialog for HTTP and SOCKS5 proxy transports (PRD §19.5). -->
<script setup lang="ts">
import { computed, ref, watch } from 'vue'

import { ApiError, createTransport, updateTransport } from '@/api/client'
import type { TransportConfig, TransportType } from '@/api/types'
import Button from '@/components/ui/Button.vue'
import Dialog from '@/components/ui/Dialog.vue'
import Input from '@/components/ui/Input.vue'
import Select from '@/components/ui/Select.vue'
import { useToast } from '@/composables/useToast'

import { validateProxyUrl, validateTransportName } from './transports'

const open = defineModel<boolean>('open', { required: true })

const props = withDefaults(
  defineProps<{
    mode: 'create' | 'edit'
    /** Transport being edited (edit mode only). */
    initial?: TransportConfig | null
    /** Names already in use, for uniqueness (must exclude the edited transport). */
    takenNames?: string[]
    /** Current config revision — passed through for optimistic concurrency. */
    revision?: string
  }>(),
  { initial: null, takenNames: () => [], revision: undefined },
)

const emit = defineEmits<{
  /** Transport was created/updated on the server. */
  saved: []
  /** 409 revision_conflict — the parent should refresh its data. */
  conflict: []
}>()

const toast = useToast()

const name = ref('')
const type = ref<string>('http')
const url = ref('')
const nameTouched = ref(false)
const urlTouched = ref(false)
const submitAttempted = ref(false)
const saving = ref(false)
const serverError = ref<string | null>(null)
const serverIssues = ref<string[]>([])

watch(open, (isOpen) => {
  if (!isOpen) return
  const initial = props.initial
  if (props.mode === 'edit' && initial !== null) {
    name.value = initial.name
    type.value = initial.type
    url.value = initial.url
  } else {
    name.value = ''
    type.value = 'http'
    url.value = ''
  }
  nameTouched.value = false
  urlTouched.value = false
  submitAttempted.value = false
  serverError.value = null
  serverIssues.value = []
})

const nameError = computed(() =>
  props.mode === 'edit' ? null : validateTransportName(name.value.trim(), props.takenNames),
)
const urlError = computed(() => validateProxyUrl(url.value.trim(), type.value))
const formValid = computed(() => nameError.value === null && urlError.value === null)

// Live validation: show errors as soon as the field has content, was
// visited, or a submit was attempted — but not on pristine empty fields.
const showNameError = computed(
  () => nameError.value !== null && (nameTouched.value || submitAttempted.value || name.value !== ''),
)
const showUrlError = computed(
  () => urlError.value !== null && (urlTouched.value || submitAttempted.value || url.value !== ''),
)

const urlPlaceholder = computed(() =>
  type.value === 'socks5' ? 'socks5://127.0.0.1:1080' : 'http://127.0.0.1:7890',
)

async function submit(): Promise<void> {
  submitAttempted.value = true
  if (!formValid.value || saving.value) return

  const initial = props.initial
  const transport: TransportConfig = {
    name: props.mode === 'edit' && initial !== null ? initial.name : name.value.trim(),
    type: type.value as TransportType,
    url: url.value.trim(),
  }

  saving.value = true
  serverError.value = null
  serverIssues.value = []
  try {
    if (props.mode === 'edit') {
      await updateTransport(transport.name, transport, props.revision)
      toast.success(`Transport "${transport.name}" updated`)
    } else {
      await createTransport(transport, props.revision)
      toast.success(`Transport "${transport.name}" created`)
    }
    emit('saved')
    open.value = false
  } catch (err) {
    if (err instanceof ApiError && err.code === 'revision_conflict') {
      serverError.value =
        'The configuration changed on the server while you were editing. Data has been refreshed — please review and save again.'
      toast.warning('Configuration changed', {
        message: 'Another writer updated the config. Refreshed to the latest revision — please retry.',
      })
      emit('conflict')
    } else {
      if (err instanceof ApiError && err.code === 'validation_failed') {
        serverError.value = err.detail !== '' ? err.detail : 'The server rejected this transport.'
        serverIssues.value = err.issues
      } else if (err instanceof ApiError) {
        serverError.value = err.detail !== '' ? err.detail : err.code
      } else {
        serverError.value = String(err)
      }
      toast.error(props.mode === 'edit' ? 'Failed to update transport' : 'Failed to create transport', {
        message: serverError.value,
      })
    }
  } finally {
    saving.value = false
  }
}
</script>

<template>
  <Dialog
    v-model:open="open"
    :title="mode === 'edit' ? 'Edit Transport' : 'New Transport'"
    :description="
      mode === 'edit'
        ? 'Change this outbound proxy link. Referencing routes pick the change up immediately.'
        : 'Declare an outbound proxy link that routes can reference.'
    "
  >
    <form class="space-y-4" novalidate @submit.prevent="submit">
      <div class="space-y-1.5">
        <label class="block space-y-1.5">
          <span class="block text-xs font-medium text-fg-secondary">Name</span>
          <Input
            v-model="name"
            mono
            placeholder="jp-proxy"
            :disabled="mode === 'edit'"
            :invalid="showNameError"
            @blur="nameTouched = true"
          />
        </label>
        <p v-if="mode === 'edit'" class="text-xs text-fg-muted">
          Names are immutable — delete and recreate to rename.
        </p>
        <p v-else-if="showNameError" class="text-xs text-danger">{{ nameError }}</p>
      </div>

      <label class="block space-y-1.5">
        <span class="block text-xs font-medium text-fg-secondary">Type</span>
        <Select v-model="type">
          <option value="http">http — HTTP proxy</option>
          <option value="socks5">socks5 — SOCKS5 proxy</option>
        </Select>
      </label>

      <div class="space-y-1.5">
        <label class="block space-y-1.5">
          <span class="block text-xs font-medium text-fg-secondary">Proxy URL</span>
          <Input
            v-model="url"
            mono
            :placeholder="urlPlaceholder"
            :invalid="showUrlError"
            @blur="urlTouched = true"
          />
        </label>
        <p v-if="showUrlError" class="text-xs text-danger">{{ urlError }}</p>
        <p v-else class="text-xs text-fg-muted">
          Scheme must match the type. Credentials (userinfo) are not supported.
        </p>
      </div>

      <div v-if="serverError !== null" class="rounded-md border border-danger/30 bg-danger-soft px-3 py-2">
        <p class="text-xs text-danger">{{ serverError }}</p>
        <ul
          v-if="serverIssues.length > 0"
          class="mt-1.5 list-inside list-disc space-y-0.5 font-mono text-xs text-danger/90"
        >
          <li v-for="issue in serverIssues" :key="issue">{{ issue }}</li>
        </ul>
      </div>

      <div class="flex items-center justify-end gap-2 pt-1">
        <Button variant="ghost" :disabled="saving" @click="open = false">Cancel</Button>
        <Button type="submit" :loading="saving" :disabled="!formValid">
          {{ mode === 'edit' ? 'Save Changes' : 'Create Transport' }}
        </Button>
      </div>
    </form>
  </Dialog>
</template>
