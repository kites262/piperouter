<!-- Transports list (PRD §19.5) — outbound links with references, tests and CRUD. -->
<script setup lang="ts">
import { Cable, FlaskConical, Pencil, Plus, RefreshCw, Trash2 } from 'lucide-vue-next'
import { computed, reactive, ref } from 'vue'

import { ApiError, deleteTransport, getConfig, listRoutes, listTransports } from '@/api/client'
import type { RouteConfig, TransportConfig } from '@/api/types'
import TransportFormDialog from '@/components/transports/TransportFormDialog.vue'
import TransportTestDialog from '@/components/transports/TransportTestDialog.vue'
import {
  formatMs,
  truncate,
  typeBadgeVariant,
  type TransportTestRecord,
} from '@/components/transports/transports'
import Badge from '@/components/ui/Badge.vue'
import Button from '@/components/ui/Button.vue'
import ConfirmDialog from '@/components/ui/ConfirmDialog.vue'
import EmptyState from '@/components/ui/EmptyState.vue'
import Table from '@/components/ui/Table.vue'
import TBody from '@/components/ui/TBody.vue'
import Td from '@/components/ui/Td.vue'
import Th from '@/components/ui/Th.vue'
import THead from '@/components/ui/THead.vue'
import Tooltip from '@/components/ui/Tooltip.vue'
import Tr from '@/components/ui/Tr.vue'
import { usePolling } from '@/composables/usePolling'
import { useToast } from '@/composables/useToast'

const toast = useToast()

// --- data -------------------------------------------------------------------

const transports = ref<TransportConfig[] | null>(null)
const routes = ref<RouteConfig[]>([])
const revision = ref('')
const loadError = ref('')
const stale = ref(false)

/** Session-local last diagnostics run per transport name (never persisted). */
const testRecords = reactive(new Map<string, TransportTestRecord>())

function errMessage(err: unknown): string {
  return err instanceof ApiError ? (err.detail !== '' ? err.detail : err.code) : String(err)
}

async function loadAll(): Promise<void> {
  try {
    const [transportList, routeList, envelope] = await Promise.all([
      listTransports(),
      listRoutes(),
      getConfig(),
    ])
    transports.value = transportList
    routes.value = routeList
    revision.value = envelope.revision
    loadError.value = ''
    stale.value = false
  } catch (err) {
    if (transports.value === null) {
      loadError.value = errMessage(err)
    } else {
      // Background refresh failed — keep showing the last data, flag it stale.
      stale.value = true
    }
  }
}

// Config data + referencing routes refresh every 5s.
const polling = usePolling(loadAll, { interval: 5000 })

function retry(): void {
  loadError.value = ''
  void polling.refresh()
}

// --- table rows ---------------------------------------------------------------

interface TransportRow {
  transport: TransportConfig
  isDirect: boolean
  typeVariant: 'accent' | 'default' | 'muted'
  endpoint: string
  /** Names of routes referencing this transport (config order). */
  refs: string[]
  test: TransportTestRecord | null
}

const rows = computed<TransportRow[]>(() => {
  const routeList = routes.value
  return (transports.value ?? []).map((transport) => {
    const isDirect = transport.type === 'direct'
    return {
      transport,
      isDirect,
      typeVariant: typeBadgeVariant(transport.type),
      endpoint: transport.url,
      refs: routeList.filter((r) => r.transport === transport.name).map((r) => r.name),
      test: testRecords.get(transport.name) ?? null,
    }
  })
})

function refsTooltip(refs: string[]): string {
  return refs.join(', ')
}

function testTooltip(test: TransportTestRecord): string {
  return `${test.method} ${truncate(test.url, 64)} — ${new Date(test.testedAt).toLocaleString()}`
}

function testFailLabel(test: TransportTestRecord): string {
  return test.errorStage !== '' ? `Failed · ${test.errorStage}` : 'Failed'
}

// --- create / edit dialog -----------------------------------------------------

const formOpen = ref(false)
const formMode = ref<'create' | 'edit'>('create')
const editing = ref<TransportConfig | null>(null)

const takenNames = computed(() => (transports.value ?? []).map((t) => t.name))

function openCreate(): void {
  formMode.value = 'create'
  editing.value = null
  formOpen.value = true
}

function openEdit(transport: TransportConfig): void {
  if (transport.type === 'direct') return
  formMode.value = 'edit'
  editing.value = { ...transport }
  formOpen.value = true
}

// --- test dialog ----------------------------------------------------------------

const testOpen = ref(false)
const testTarget = ref<TransportConfig | null>(null)

function openTest(transport: TransportConfig): void {
  testTarget.value = transport
  testOpen.value = true
}

const testInitial = computed(() => {
  const target = testTarget.value
  const previous = target !== null ? (testRecords.get(target.name) ?? null) : null
  return { url: previous?.url ?? '', method: previous?.method ?? 'GET' }
})

function onTestResult(name: string, record: TransportTestRecord): void {
  testRecords.set(name, record)
}

// --- delete ----------------------------------------------------------------------

const deleteOpen = ref(false)
const deleteTarget = ref<TransportConfig | null>(null)
const deleting = ref(false)

function requestDelete(transport: TransportConfig): void {
  if (transport.type === 'direct') return
  deleteTarget.value = transport
  deleteOpen.value = true
}

async function confirmDelete(): Promise<void> {
  const target = deleteTarget.value
  if (target === null || deleting.value) return
  deleting.value = true
  try {
    await deleteTransport(target.name, revision.value)
    toast.success(`Transport "${target.name}" deleted`)
    deleteOpen.value = false
    await polling.refresh()
  } catch (err) {
    deleteOpen.value = false
    if (err instanceof ApiError && err.code === 'transport_in_use') {
      // 409: the server's detail lists the referencing route names.
      toast.error(`Transport "${target.name}" is in use`, {
        message:
          err.detail !== ''
            ? err.detail
            : 'One or more routes still reference this transport. Repoint or delete them first.',
        duration: 8000,
      })
    } else if (err instanceof ApiError && err.code === 'revision_conflict') {
      toast.warning('Configuration changed', {
        message: 'The config was modified elsewhere — reloading the current state.',
      })
      void polling.refresh()
    } else {
      toast.error(`Failed to delete "${target.name}"`, { message: errMessage(err) })
    }
  } finally {
    deleting.value = false
  }
}
</script>

<template>
  <section class="space-y-6">
    <div class="flex items-start justify-between gap-4 animate-fade-up">
      <div>
        <h1 class="text-lg font-semibold tracking-tight text-fg">Transports</h1>
        <p class="mt-1 text-sm text-fg-muted">
          Outbound links: built-in direct, HTTP proxy and SOCKS5 proxy.
        </p>
      </div>
      <Button @click="openCreate">
        <Plus class="h-4 w-4" />
        New Transport
      </Button>
    </div>

    <div v-if="transports === null && loadError === ''" class="glass-panel p-4" aria-busy="true">
      <div class="animate-pulse space-y-3">
        <div class="h-4 w-1/3 rounded bg-surface-raised" />
        <div v-for="i in 4" :key="i" class="h-9 rounded bg-surface-raised/70" />
      </div>
    </div>

    <div
      v-else-if="transports === null"
      class="glass-panel flex flex-col items-center gap-3 px-6 py-12 text-center"
    >
      <p class="text-sm font-medium text-danger">Failed to load transports</p>
      <p class="max-w-md break-all font-mono text-xs text-fg-muted">{{ loadError }}</p>
      <Button variant="outline" size="sm" @click="retry">
        <RefreshCw class="h-3.5 w-3.5" />
        Retry
      </Button>
    </div>

    <!-- Empty state (defensive — the built-in direct transport is always listed) -->
    <EmptyState
      v-else-if="transports.length === 0"
      title="No transports available"
      description="Declare an HTTP or SOCKS5 proxy that routes can send traffic through."
    >
      <template #icon><Cable class="h-5 w-5" /></template>
      <Button size="sm" @click="openCreate">
        <Plus class="h-4 w-4" />
        New Transport
      </Button>
    </EmptyState>

    <div v-else class="animate-fade-up stagger-2 space-y-2">
      <p v-if="stale" class="text-xs text-warning">
        Refresh failed — showing the last loaded data, retrying every 5s.
      </p>
      <Table>
        <colgroup>
          <col class="w-[18%]" />
          <col class="w-[10%]" />
          <col />
          <col class="w-[14%]" />
          <col class="w-[14%]" />
          <!-- Room for 3× size-sm icon buttons + cell padding (match Routes edge inset). -->
          <col class="w-[9.5rem]" />
        </colgroup>
        <THead>
          <tr>
            <Th>Name</Th>
            <Th>Type</Th>
            <Th>Proxy Endpoint</Th>
            <Th>Referenced by</Th>
            <Th>Last test</Th>
            <Th><span class="sr-only">Actions</span></Th>
          </tr>
        </THead>
        <TBody>
          <Tr v-for="row in rows" :key="row.transport.name">
            <Td>
              <div class="flex min-w-0 items-center gap-2">
                <span class="truncate font-medium text-fg">{{ row.transport.name }}</span>
                <Badge v-if="row.isDirect" variant="muted" class="shrink-0">built-in</Badge>
              </div>
            </Td>
            <Td>
              <Badge :variant="row.typeVariant" mono>{{ row.transport.type }}</Badge>
            </Td>
            <Td>
              <Tooltip v-if="row.endpoint !== ''" :text="row.endpoint">
                <span class="block truncate font-mono text-xs text-fg-secondary">
                  {{ row.endpoint }}
                </span>
              </Tooltip>
              <span v-else class="text-xs text-fg-muted">—</span>
            </Td>
            <Td>
              <Tooltip v-if="row.refs.length > 0" :text="refsTooltip(row.refs)">
                <span
                  class="text-xs tabular-nums text-fg-secondary underline decoration-border-strong decoration-dotted underline-offset-4"
                >
                  {{ row.refs.length }} route{{ row.refs.length === 1 ? '' : 's' }}
                </span>
              </Tooltip>
              <span v-else class="text-xs text-fg-muted">0 routes</span>
            </Td>
            <Td>
              <Tooltip v-if="row.test !== null" :text="testTooltip(row.test)">
                <Badge v-if="row.test.ok" variant="success" mono>
                  OK · {{ formatMs(row.test.totalDurationMs) }}
                </Badge>
                <Badge v-else variant="danger" mono>{{ testFailLabel(row.test) }}</Badge>
              </Tooltip>
              <Badge v-else variant="muted">untested</Badge>
            </Td>
            <Td>
              <!-- Same action chrome as Routes: size-sm ghost buttons, flex end, gap-1. -->
              <div class="flex items-center justify-end gap-1">
                <Button
                  variant="ghost"
                  size="sm"
                  :aria-label="`Test transport ${row.transport.name}`"
                  @click="openTest(row.transport)"
                >
                  <FlaskConical class="h-3.5 w-3.5" />
                </Button>
                <Tooltip v-if="row.isDirect" text="Built-in transport — read-only">
                  <Button
                    variant="ghost"
                    size="sm"
                    disabled
                    aria-label="Edit transport (disabled)"
                  >
                    <Pencil class="h-3.5 w-3.5" />
                  </Button>
                </Tooltip>
                <Button
                  v-else
                  variant="ghost"
                  size="sm"
                  :aria-label="`Edit transport ${row.transport.name}`"
                  @click="openEdit(row.transport)"
                >
                  <Pencil class="h-3.5 w-3.5" />
                </Button>
                <Tooltip v-if="row.isDirect" text="Built-in transport — cannot be deleted">
                  <Button
                    variant="ghost"
                    size="sm"
                    disabled
                    aria-label="Delete transport (disabled)"
                  >
                    <Trash2 class="h-3.5 w-3.5" />
                  </Button>
                </Tooltip>
                <Button
                  v-else
                  variant="ghost"
                  size="sm"
                  class="hover:text-danger"
                  :aria-label="`Delete transport ${row.transport.name}`"
                  @click="requestDelete(row.transport)"
                >
                  <Trash2 class="h-3.5 w-3.5" />
                </Button>
              </div>
            </Td>
          </Tr>
        </TBody>
      </Table>
    </div>

    <TransportFormDialog
      v-model:open="formOpen"
      :mode="formMode"
      :initial="editing"
      :taken-names="takenNames"
      :revision="revision"
      @saved="() => void polling.refresh()"
      @conflict="() => void polling.refresh()"
    />

    <TransportTestDialog
      v-model:open="testOpen"
      :transport="testTarget"
      :initial-url="testInitial.url"
      :initial-method="testInitial.method"
      @result="onTestResult"
    />

    <ConfirmDialog
      v-model:open="deleteOpen"
      title="Delete transport"
      :message="
        deleteTarget
          ? `Delete transport “${deleteTarget.name}” (${deleteTarget.url})? Routes referencing it will block the delete.`
          : ''
      "
      confirm-text="Delete"
      danger
      :loading="deleting"
      @confirm="confirmDelete"
    />
  </section>
</template>
