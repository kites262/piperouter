<script setup lang="ts">
import { Copy, Pencil, Plus, RefreshCw, Trash2 } from 'lucide-vue-next'
import { computed, onMounted, reactive, ref } from 'vue'
import { RouterLink } from 'vue-router'

import { ApiError, getConfig, putConfig } from '@/api/client'
import { normalizeOverwriteRule, overwritePhaseLabel } from '@/api/overwrite'
import type { Config, ConfigEnvelope, OverwriteRule } from '@/api/types'
import { NAME_RE } from '@/components/routes/validation'
import Button from '@/components/ui/Button.vue'
import ConfirmDialog from '@/components/ui/ConfirmDialog.vue'
import Dialog from '@/components/ui/Dialog.vue'
import EmptyState from '@/components/ui/EmptyState.vue'
import Input from '@/components/ui/Input.vue'
import Select from '@/components/ui/Select.vue'
import Switch from '@/components/ui/Switch.vue'
import Table from '@/components/ui/Table.vue'
import TBody from '@/components/ui/TBody.vue'
import Td from '@/components/ui/Td.vue'
import Th from '@/components/ui/Th.vue'
import THead from '@/components/ui/THead.vue'
import Tr from '@/components/ui/Tr.vue'
import { useToast } from '@/composables/useToast'

const toast = useToast()
const envelope = ref<ConfigEnvelope | null>(null)
const loadError = ref('')
const saving = ref(false)
const refreshing = ref(false)
const mutationError = ref('')
const conflict = ref(false)
const rules = computed(() => (envelope.value?.config.overwrite?.rules ?? []).map(normalizeOverwriteRule))
const enabled = computed(() => envelope.value?.config.overwrite?.enabled ?? false)
const proxyRoutes = computed(() => envelope.value?.config.routes.filter((r) => r.type === 'proxy') ?? [])

function message(err: unknown): string {
  if (err instanceof ApiError) return err.issues.length ? err.issues.join('\n') : err.detail || err.code
  return String(err)
}

async function load(): Promise<void> {
  refreshing.value = true
  try {
    envelope.value = await getConfig()
    loadError.value = ''
    mutationError.value = ''
    conflict.value = false
  } catch (err) {
    loadError.value = message(err)
  } finally {
    refreshing.value = false
  }
}
onMounted(() => void load())

function draftConfig(): Config {
  return JSON.parse(JSON.stringify(envelope.value!.config)) as Config
}

async function reloadAfterConflict(): Promise<void> {
  // Reusing stale route selections with a fresh revision can silently unbind
  // routes added by another editor. Reopen a draft from the latest snapshot.
  editorOpen.value = false
  deleteOpen.value = false
  await load()
}

async function commit(config: Config): Promise<boolean> {
  if (saving.value || refreshing.value || !envelope.value) return false
  saving.value = true
  mutationError.value = ''
  conflict.value = false
  try {
    await putConfig({ revision: envelope.value.revision, config })
    // Keep the successfully saved data visible even if the following refresh fails.
    envelope.value = { ...envelope.value, config }
    await load()
    toast.success('Overwrite configuration saved')
    return true
  } catch (err) {
    mutationError.value = message(err)
    conflict.value = err instanceof ApiError && err.code === 'revision_conflict'
    return false
  } finally {
    saving.value = false
  }
}

async function setGlobal(value: boolean): Promise<void> {
  const config = draftConfig()
  config.overwrite = { enabled: value, rules: config.overwrite?.rules ?? [] }
  await commit(config)
}

async function setRuleEnabled(rule: OverwriteRule, value: boolean): Promise<void> {
  const config = draftConfig()
  config.overwrite!.rules.find((r) => r.name === rule.name)!.enabled = value
  await commit(config)
}

function boundRoutes(name: string): string[] {
  return proxyRoutes.value.filter((r) => r.options.overwrites?.includes(name)).map((r) => r.name)
}

const editorOpen = ref(false)
const originalName = ref<string | null>(null)
const form = reactive({
  name: '', enabled: false,
  condition: { phase: 'req' as const, path: '.model', op: 'eq' as const, value: '' },
  action: { phase: 'req' as const, path: '.model', op: 'set' as const, value: '' },
  routes: [] as string[],
})
const submitted = ref(false)
const formError = computed(() => {
  if (!NAME_RE.test(form.name.trim())) return 'Name must start with a letter or digit and use letters, digits, dots, underscores or hyphens (max 64 characters).'
  if (rules.value.some((r) => r.name === form.name.trim() && r.name !== originalName.value)) return 'A rule with this name already exists.'
  if (!form.condition.path.startsWith('.')) return 'Condition JSON path must start with . for the complete body.'
  if (!form.action.path.startsWith('.')) return 'Action JSON path must start with . for the complete body.'
  return ''
})

function edit(rule?: OverwriteRule, copy = false): void {
  if (rule) rule = normalizeOverwriteRule(rule)
  originalName.value = rule && !copy ? rule.name : null
  let newName = ''
  if (rule) {
    newName = rule.name
    if (copy) {
      let suffix = 1
      do { newName = `${rule.name.slice(0, 54)}-copy-${suffix++}` } while (rules.value.some((r) => r.name === newName))
    }
  }
  Object.assign(form, {
    name: newName,
    enabled: rule && !copy ? rule.enabled : false,
    condition: { ...(rule?.condition ?? { phase: 'req', path: '.model', op: 'eq', value: '' }) },
    action: { ...(rule?.action ?? { phase: 'req', path: '.model', op: 'set', value: '' }) },
    routes: rule && !copy ? boundRoutes(rule.name) : [],
  })
  submitted.value = false
  mutationError.value = ''
  editorOpen.value = true
}

function currentRule(): OverwriteRule {
  return { name: form.name.trim(), enabled: form.enabled, condition: { ...form.condition }, action: { ...form.action } }
}

async function saveRule(): Promise<void> {
  submitted.value = true
  if (formError.value || saving.value) return
  const config = draftConfig()
  config.overwrite ??= { enabled: false, rules: [] }
  config.overwrite.rules ??= []
  const rule = currentRule()
  const index = config.overwrite.rules.findIndex((r) => r.name === originalName.value)
  if (index < 0) config.overwrite.rules.push(rule)
  else config.overwrite.rules[index] = rule
  for (const route of config.routes) {
    if (route.type !== 'proxy') continue
    const bound = route.options.overwrites ?? []
    const selected = form.routes.includes(route.name)
    // Rename in place so a rule keeps its position in each route's binding list.
    route.options.overwrites = bound.flatMap((name) => name === originalName.value ? (selected ? [rule.name] : []) : [name])
    if (selected && !route.options.overwrites.includes(rule.name)) route.options.overwrites.push(rule.name)
  }
  if (await commit(config)) editorOpen.value = false
}

const deleteOpen = ref(false)
const deleteTarget = ref<OverwriteRule | null>(null)
function askDelete(rule: OverwriteRule): void {
  deleteTarget.value = rule
  mutationError.value = ''
  deleteOpen.value = true
}
async function removeRule(): Promise<void> {
  if (!deleteTarget.value) return
  const name = deleteTarget.value.name
  const config = draftConfig()
  config.overwrite!.rules = config.overwrite!.rules.filter((r) => r.name !== name)
  for (const route of config.routes) {
    if (route.type === 'proxy') route.options.overwrites = (route.options.overwrites ?? []).filter((n) => n !== name)
  }
  if (await commit(config)) deleteOpen.value = false
}

</script>

<template>
  <div class="space-y-5">
    <header class="flex flex-wrap items-center justify-between gap-3">
      <div>
        <h1 class="text-lg font-semibold tracking-tight text-fg">Overwrite</h1>
        <p class="mt-1 text-sm text-fg-muted">Apply JSON actions when conditions match on bound Routes.</p>
      </div>
      <div class="flex items-center gap-2">
        <Button variant="outline" size="sm" :disabled="saving || refreshing || editorOpen" @click="load"><RefreshCw class="h-3.5 w-3.5" />Refresh</Button>
        <Button size="sm" :disabled="!envelope || saving || refreshing" @click="edit()"><Plus class="h-3.5 w-3.5" />New Rule</Button>
      </div>
    </header>

    <div v-if="loadError" role="alert" class="glass-panel border-danger/30 p-4 text-sm text-danger">{{ loadError }}</div>
    <div v-if="mutationError && !editorOpen && !deleteOpen" role="alert" class="glass-panel space-y-2 border-danger/30 p-4">
      <p class="whitespace-pre-wrap text-sm text-danger">{{ mutationError }}</p>
      <Button v-if="conflict" variant="outline" size="sm" @click="load">Reload latest configuration</Button>
    </div>
    <div v-if="!envelope && !loadError" class="glass-panel p-4 text-sm text-fg-muted" aria-busy="true">Loading configuration…</div>
    <template v-if="envelope">
      <div class="glass-panel flex items-center justify-between gap-4 p-4">
        <div>
          <p class="text-sm font-medium text-fg">Enable Overwrite</p>
          <p class="mt-1 text-xs text-fg-muted">{{ enabled ? 'Enabled rules apply to new requests on their bound Routes.' : 'Disabled. Rules can be configured without affecting traffic.' }}</p>
        </div>
        <Switch :model-value="enabled" :disabled="saving || refreshing" aria-label="Enable Overwrite" @update:model-value="setGlobal" />
      </div>
      <EmptyState v-if="rules.length === 0" title="No overwrite rules" description="Create a replacement rule, then bind it to Routes.">
        <Button size="sm" :disabled="saving || refreshing" @click="edit()"><Plus class="h-3.5 w-3.5" />New Rule</Button>
      </EmptyState>
      <div v-else class="overwrite-list min-w-0">
        <Table class="overwrite-table">
          <colgroup>
            <col class="w-64" />
            <col class="w-[4.5rem]" />
            <col />
            <col class="w-[4.5rem]" />
            <col class="w-28" />
          </colgroup>
          <THead><tr><Th>Rule</Th><Th>Enabled</Th><Th>Condition / Action</Th><Th>Routes</Th><Th class="text-right">Actions</Th></tr></THead>
          <TBody>
            <Tr v-for="rule in rules" :key="rule.name" class="overwrite-row">
              <Td class="overwrite-rule">
                <div class="flex min-w-0 items-center gap-2">
                  <button :title="rule.name" class="min-w-0 truncate text-left font-mono text-sm text-accent hover:underline disabled:opacity-50" :disabled="saving || refreshing" @click="edit(rule)">{{ rule.name }}</button>
                </div>
              </Td>
              <Td class="overwrite-enabled"><Switch :model-value="rule.enabled" :disabled="saving || refreshing" :aria-label="`Enable rule ${rule.name}`" @update:model-value="setRuleEnabled(rule, $event)" /></Td>
              <Td class="overwrite-summary">
                <div class="grid min-w-0 grid-cols-[1rem_2.25rem_minmax(0,1fr)] items-center gap-x-2 font-mono text-xs leading-5">
                  <span aria-hidden="true" class="text-fg-muted">if</span>
                  <span class="rounded border border-border bg-surface-raised text-center text-[10px] leading-4 text-fg-muted"><span class="sr-only">Condition phase: </span>{{ overwritePhaseLabel(rule.condition.phase) }}</span>
                  <span :title="`${overwritePhaseLabel(rule.condition.phase)} ${rule.condition.path} ${rule.condition.op} ${JSON.stringify(rule.condition.value)}`" class="truncate text-fg"><span class="sr-only">Condition: </span>{{ rule.condition.path }} {{ rule.condition.op }} {{ JSON.stringify(rule.condition.value) }}</span>
                  <span aria-hidden="true" class="text-fg-muted">→</span>
                  <span class="rounded border border-border bg-surface-raised text-center text-[10px] leading-4 text-fg-muted"><span class="sr-only">Action phase: </span>{{ overwritePhaseLabel(rule.action.phase) }}</span>
                  <span :title="`${overwritePhaseLabel(rule.action.phase)} ${rule.action.path} ${rule.action.op} ${JSON.stringify(rule.action.value)}`" class="truncate text-fg-secondary"><span class="sr-only">Action: </span>{{ rule.action.path }} {{ rule.action.op }} {{ JSON.stringify(rule.action.value) }}</span>
                </div>
              </Td>
              <Td class="overwrite-routes"><span class="overwrite-route-label text-xs text-fg-muted">Routes · </span><span :title="boundRoutes(rule.name).join(', ')" class="whitespace-nowrap text-sm text-fg-secondary">{{ boundRoutes(rule.name).length }} bound</span></Td>
              <Td class="overwrite-actions"><div class="flex justify-end gap-0.5">
                <Button variant="ghost" size="sm" class="overwrite-action" :disabled="saving || refreshing" :aria-label="`Edit ${rule.name}`" @click="edit(rule)"><Pencil class="h-3.5 w-3.5" /></Button>
                <Button variant="ghost" size="sm" class="overwrite-action" :disabled="saving || refreshing" :aria-label="`Copy ${rule.name}`" @click="edit(rule, true)"><Copy class="h-3.5 w-3.5" /></Button>
                <Button variant="ghost" size="sm" class="overwrite-action" :disabled="saving || refreshing" :aria-label="`Delete ${rule.name}`" @click="askDelete(rule)"><Trash2 class="h-3.5 w-3.5 text-danger" /></Button>
              </div></Td>
            </Tr>
          </TBody>
        </Table>
      </div>
      <p class="text-xs text-fg-muted">Conditions match the original body. Actions run in the order of each Route's bindings. Test saved rules in <RouterLink to="/diagnostics" class="text-accent hover:underline">Diagnostics</RouterLink>.</p>
    </template>

    <Dialog v-model:open="editorOpen" :title="originalName ? 'Edit Overwrite Rule' : 'New Overwrite Rule'" description="Save changes to apply them to new requests immediately." max-width="max-w-5xl">
      <form class="space-y-4" @submit.prevent="saveRule">
        <label class="block space-y-1.5"><span class="text-xs font-medium text-fg-secondary">Name</span><Input v-model="form.name" placeholder="model-alias" mono /></label>
        <fieldset class="space-y-3 rounded-md border border-border p-3">
          <legend class="px-1 text-sm font-medium text-fg">Condition</legend>
          <div class="overwrite-editor-row grid grid-cols-1 gap-3 sm:grid-cols-2 lg:grid-cols-[6rem_minmax(0,1fr)_8rem_minmax(0,1.4fr)]">
            <label class="block min-w-0 space-y-1.5"><span class="text-xs font-medium text-fg-secondary">Phase</span><Select v-model="form.condition.phase"><option value="req">Req</option></Select></label>
            <label class="block min-w-0 space-y-1.5"><span class="text-xs font-medium text-fg-secondary">JSON path</span><Input v-model="form.condition.path" placeholder=".model" mono /></label>
            <label class="block min-w-0 space-y-1.5"><span class="text-xs font-medium text-fg-secondary">Operator</span><Select v-model="form.condition.op"><option value="eq">eq</option></Select></label>
            <label class="block min-w-0 space-y-1.5"><span class="text-xs font-medium text-fg-secondary">Value</span><Input v-model="form.condition.value" placeholder="gpt-6.1-sol" spellcheck="false" mono /></label>
          </div>
        </fieldset>
        <fieldset class="space-y-3 rounded-md border border-border p-3">
          <legend class="px-1 text-sm font-medium text-fg">Action</legend>
          <div class="overwrite-editor-row grid grid-cols-1 gap-3 sm:grid-cols-2 lg:grid-cols-[6rem_minmax(0,1fr)_8rem_minmax(0,1.4fr)]">
            <label class="block min-w-0 space-y-1.5"><span class="text-xs font-medium text-fg-secondary">Phase</span><Select v-model="form.action.phase"><option value="req">Req</option></Select></label>
            <label class="block min-w-0 space-y-1.5"><span class="text-xs font-medium text-fg-secondary">JSON path</span><Input v-model="form.action.path" placeholder=".model" mono /></label>
            <label class="block min-w-0 space-y-1.5"><span class="text-xs font-medium text-fg-secondary">Operation</span><Select v-model="form.action.op"><option value="set">set</option></Select></label>
            <label class="block min-w-0 space-y-1.5"><span class="text-xs font-medium text-fg-secondary">Value</span><Input v-model="form.action.value" placeholder="gpt-6.1-sol-1" spellcheck="false" mono /></label>
          </div>
        </fieldset>
        <p class="text-xs text-fg-muted">The first . in a JSON path represents the complete body, for example .model or .messages[0].model.</p>
        <p class="text-xs text-fg-muted">Values are case-sensitive. Empty strings and surrounding spaces are preserved.</p>
        <fieldset class="space-y-2">
          <legend class="text-xs font-medium text-fg-secondary">Bind to Routes</legend>
          <div v-if="proxyRoutes.length" class="grid max-h-40 gap-2 overflow-y-auto rounded-md border border-border p-3 sm:grid-cols-2">
            <label v-for="route in proxyRoutes" :key="route.name" class="flex items-center gap-2 text-sm text-fg-secondary"><input v-model="form.routes" type="checkbox" :value="route.name" class="accent-accent" /><span class="truncate font-mono">{{ route.name }}</span><span v-if="!route.enabled" class="text-xs text-fg-muted">disabled</span></label>
          </div>
          <p v-else class="text-xs text-fg-muted">No proxy Routes yet. <RouterLink to="/routes" class="text-accent hover:underline">Create a Route</RouterLink> to bind this rule later.</p>
        </fieldset>
        <p v-if="!enabled" class="rounded-md border border-warning/30 bg-warning-soft p-3 text-xs text-warning">Overwrite is globally disabled. Saved rules will take effect after you turn it on.</p>
        <p v-if="submitted && formError" role="alert" class="text-xs text-danger">{{ formError }}</p>
        <div v-if="mutationError" role="alert" class="space-y-2"><p class="whitespace-pre-wrap text-xs text-danger">{{ mutationError }}</p><Button v-if="conflict" variant="outline" size="sm" :disabled="saving" @click="reloadAfterConflict">Reload and close draft</Button></div>
      </form>
      <template #footer><Button variant="ghost" :disabled="saving" @click="editorOpen = false">Cancel</Button><Button :loading="saving" :disabled="refreshing" @click="saveRule">Save Rule</Button></template>
    </Dialog>

    <ConfirmDialog v-model:open="deleteOpen" title="Delete Overwrite Rule" :message="`Delete “${deleteTarget?.name ?? ''}” and remove its bindings from all Routes?`" confirm-text="Delete Rule" danger :loading="saving" @confirm="removeRule">
      <div v-if="mutationError" class="mt-3 space-y-2"><p role="alert" class="whitespace-pre-wrap text-xs text-danger">{{ mutationError }}</p><Button v-if="conflict" variant="outline" size="sm" :disabled="saving" @click="reloadAfterConflict">Reload and close draft</Button></div>
    </ConfirmDialog>
  </div>
</template>

<style scoped>
.overwrite-list {
  container: overwrite-list / inline-size;
}

.overwrite-action {
  width: 1.75rem;
  height: 1.75rem;
  padding: 0;
}

.overwrite-route-label {
  display: none;
}

@container overwrite-list (width < 52rem) {
  .overwrite-table :deep(table),
  .overwrite-table :deep(tbody) {
    display: block;
  }

  .overwrite-table :deep(colgroup) {
    display: none;
  }

  /* Keep column headings available to assistive technology in the card layout. */
  .overwrite-table :deep(thead) {
    position: absolute;
    width: 1px;
    height: 1px;
    padding: 0;
    margin: -1px;
    overflow: hidden;
    clip-path: inset(50%);
    white-space: nowrap;
    border: 0;
  }

  .overwrite-table :deep(.overwrite-row) {
    display: grid;
    grid-template-columns: minmax(0, 1fr) 2.25rem 5.5rem;
    grid-template-areas:
      'rule enabled actions'
      'summary summary summary'
      'routes routes routes';
    gap: 0.75rem 0.5rem;
    align-items: center;
    padding: 0.75rem;
  }

  .overwrite-table :deep(td) {
    padding: 0;
  }

  .overwrite-rule {
    grid-area: rule;
  }

  .overwrite-enabled {
    grid-area: enabled;
  }

  .overwrite-summary {
    grid-area: summary;
  }

  .overwrite-routes {
    grid-area: routes;
  }

  .overwrite-actions {
    grid-area: actions;
  }

  .overwrite-route-label {
    display: inline;
  }
}
</style>
