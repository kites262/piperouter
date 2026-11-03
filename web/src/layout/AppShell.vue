<script setup lang="ts">
import {
  Activity,
  Cable,
  LayoutDashboard,
  Moon,
  Route as RouteIcon,
  ScrollText,
  Settings,
  Shield,
  ShieldAlert,
  ShieldCheck,
  Sun,
  Waypoints,
} from 'lucide-vue-next'
import { computed, nextTick, onBeforeUnmount, onMounted, provide, ref, watch } from 'vue'
import type { ComponentPublicInstance } from 'vue'
import { RouterLink, RouterView, useRoute } from 'vue-router'

import { ApiError, getAccessToken, setAccessToken, getStatus } from '@/api/client'
import type { StatusResponse } from '@/api/types'
import ConfigErrorBanner from '@/components/ConfigErrorBanner.vue'
import StatusDot from '@/components/ui/StatusDot.vue'
import { usePolling } from '@/composables/usePolling'
import { statusKey } from '@/composables/useStatus'
import { useTheme } from '@/composables/useTheme'

const route = useRoute()
const { theme, toggle: toggleTheme } = useTheme()

// Primary navigation; Settings is pinned separately at the bottom of the
// sidebar so day-to-day views are grouped apart from configuration.
const primaryNav = [
  { to: '/', label: 'Dashboard', icon: LayoutDashboard },
  { to: '/routes', label: 'Routes', icon: RouteIcon },
  { to: '/transports', label: 'Transports', icon: Cable },
  { to: '/logs', label: 'Logs', icon: ScrollText },
  { to: '/diagnostics', label: 'Diagnostics', icon: Activity },
] as const

function isActive(to: string): boolean {
  if (to === '/') return route.path === '/'
  return route.path === to || route.path.startsWith(`${to}/`)
}

// --- sliding selection indicator ------------------------------------------
// A single pill slides behind the active primary-nav item. Its geometry is
// measured from the active link so it tracks any layout; the spring transition
// lives in .nav-indicator (disabled under prefers-reduced-motion).
const itemEls = ref<HTMLElement[]>([])
const indicator = ref({ top: 0, height: 0, left: 0, width: 0, visible: false })

function captureItem(el: Element | ComponentPublicInstance | null, index: number): void {
  if (!el) return
  const node = ('$el' in el ? (el as ComponentPublicInstance).$el : el) as unknown
  if (node instanceof HTMLElement) itemEls.value[index] = node
}

function updateIndicator(): void {
  const idx = primaryNav.findIndex((n) => isActive(n.to))
  const el = idx >= 0 ? itemEls.value[idx] : undefined
  if (!el) {
    indicator.value = { ...indicator.value, visible: false }
    return
  }
  indicator.value = {
    top: el.offsetTop,
    height: el.offsetHeight,
    left: el.offsetLeft,
    width: el.offsetWidth,
    visible: true,
  }
}

watch(
  () => route.path,
  () => void nextTick(updateIndicator),
)
onMounted(() => {
  void nextTick(updateIndicator)
  window.addEventListener('resize', updateIndicator)
})
onBeforeUnmount(() => window.removeEventListener('resize', updateIndicator))

const pageTitle = computed(() => route.meta.title ?? 'PipeRouter')

// --- shared status poll (every 5s), provided to banner/pages -------------
const status = ref<StatusResponse | null>(null)
const statusError = ref<string | null>(null)
const authRequired = ref(false)
const tokenInput = ref(getAccessToken())
const authRevision = ref(0)

function applyToken(): void {
  setAccessToken(tokenInput.value.trim())
  tokenInput.value = getAccessToken()
  authRevision.value++
  status.value = null
  statusError.value = null
  authRequired.value = false
  void fetchStatus()
}

function clearToken(): void {
  tokenInput.value = ''
  applyToken()
}

async function fetchStatus(): Promise<void> {
  const revision = authRevision.value
  try {
    const result = await getStatus()
    if (revision !== authRevision.value) return
    status.value = result
    statusError.value = null
    authRequired.value = false
  } catch (err) {
    if (revision !== authRevision.value) return
    status.value = null
    authRequired.value = err instanceof ApiError && err.status === 401
    statusError.value =
      err instanceof ApiError ? (err.detail !== '' ? err.detail : err.code) : String(err)
  }
}

const { refresh } = usePolling(fetchStatus, { interval: 5000 })
provide(statusKey, { status, error: statusError, refresh })

const chip = computed<{ tone: 'success' | 'warning' | 'danger' | 'muted'; label: string }>(() => {
  if (authRequired.value) return { tone: 'danger', label: 'Token required or invalid' }
  if (statusError.value !== null) return { tone: 'danger', label: 'Admin unreachable' }
  if (status.value === null) return { tone: 'muted', label: 'Connecting…' }
  if (!status.value.config.valid) return { tone: 'warning', label: 'Config invalid' }
  return { tone: 'success', label: 'Config healthy' }
})

const shortRevision = computed(() => {
  const revision = status.value?.config.revision ?? ''
  if (revision === '') return ''
  return revision.startsWith('sha256:') ? revision.slice(7, 19) : revision.slice(0, 12)
})

// --- admin bind posture (replaces the old static "loopback only" note) ----
// Reflects the REAL admin listen address from /status: loopback is safe;
// anything else means the unauthenticated admin plane is reachable off-box.
function adminHost(addr: string): string {
  const ipv6 = addr.match(/^\[(.+)]:\d+$/)
  if (ipv6) return ipv6[1]
  const idx = addr.lastIndexOf(':')
  return idx >= 0 ? addr.slice(0, idx) : addr
}
function isLoopbackAddr(addr: string): boolean {
  const h = adminHost(addr)
  return h === 'localhost' || h === '::1' || h.startsWith('127.')
}
const adminPosture = computed(() => {
  const listen = status.value?.admin.listen ?? ''
  if (listen === '') return { tone: 'text-fg-muted', icon: Shield, label: 'Admin · status unknown' }
  return isLoopbackAddr(listen)
    ? { tone: 'text-success', icon: ShieldCheck, label: 'Admin · loopback only' }
    : { tone: 'text-warning', icon: ShieldAlert, label: 'Admin · exposed to network' }
})
</script>

<template>
  <div class="flex h-screen overflow-hidden">
    <!-- Sidebar — liquid glass -->
    <aside class="glass z-20 flex w-64 shrink-0 flex-col border-y-0 border-l-0">
      <div class="flex h-14 items-center gap-2.5 border-b border-border px-4">
        <span
          class="logo-glow flex h-8 w-8 items-center justify-center rounded-lg border border-accent/20 bg-accent-soft text-accent elevate"
        >
          <Waypoints class="h-4.5 w-4.5" />
        </span>
        <div class="leading-tight">
          <p class="text-sm font-semibold tracking-tight text-fg">PipeRouter</p>
          <p class="text-[10px] uppercase tracking-[0.14em] text-fg-muted">Admin Console</p>
        </div>
      </div>

      <nav class="relative flex-1 space-y-2 overflow-y-auto p-3">
        <span
          class="nav-indicator pointer-events-none absolute top-0 z-0 rounded-xl bg-accent-soft"
          :class="indicator.visible ? 'opacity-100' : 'opacity-0'"
          :style="{
            transform: `translateY(${indicator.top}px)`,
            height: `${indicator.height}px`,
            left: `${indicator.left}px`,
            width: `${indicator.width}px`,
            boxShadow:
              'inset 2px 0 0 0 var(--color-accent), 0 0 20px -8px rgb(109 124 255 / 0.45)',
          }"
          aria-hidden="true"
        />
        <RouterLink
          v-for="(item, i) in primaryNav"
          :key="item.to"
          :ref="(el) => captureItem(el as Element | ComponentPublicInstance | null, i)"
          :to="item.to"
          class="hero-button relative z-10"
          :class="
            isActive(item.to)
              ? 'is-active text-accent'
              : 'text-fg-secondary hover:text-fg'
          "
        >
          <span class="hero-button-icon">
            <component :is="item.icon" class="h-4.5 w-4.5" />
          </span>
          <span class="min-w-0 flex-1 truncate">{{ item.label }}</span>
        </RouterLink>
      </nav>

      <div class="space-y-2 border-t border-border p-3">
        <form class="space-y-2 px-3 pb-2" @submit.prevent="applyToken">
          <label for="access-token" class="block text-xs font-medium text-fg-secondary">Access Token</label>
          <input
            id="access-token"
            v-model="tokenInput"
            type="password"
            autocomplete="off"
            spellcheck="false"
            placeholder="Enter Access Token"
            class="w-full rounded-lg border border-border bg-transparent px-2 py-1.5 text-xs text-fg outline-none focus:border-accent"
            :aria-invalid="authRequired"
            aria-describedby="access-token-help"
          />
          <div class="flex gap-2">
            <button type="submit" class="rounded-lg bg-accent-soft px-3 py-1.5 text-xs text-accent">Apply</button>
            <button type="button" class="rounded-lg px-3 py-1.5 text-xs text-fg-secondary hover:text-fg" @click="clearToken">Clear</button>
          </div>
          <p id="access-token-help" class="text-[11px]" :class="authRequired ? 'text-danger' : 'text-fg-muted'" role="status">
            {{ authRequired ? 'Token required or invalid. Enter a valid token and apply.' : 'Stored for this tab’s session only.' }}
          </p>
        </form>
        <RouterLink
          to="/settings"
          class="hero-button"
          :class="
            isActive('/settings')
              ? 'is-active bg-accent-soft text-accent'
              : 'text-fg-secondary hover:text-fg'
          "
        >
          <span class="hero-button-icon">
            <Settings class="h-4.5 w-4.5" />
          </span>
          <span class="min-w-0 flex-1 truncate">Settings</span>
        </RouterLink>
        <button
          type="button"
          class="hero-button text-fg-secondary hover:text-fg"
          :title="`Switch to ${theme === 'dark' ? 'light' : 'dark'} theme`"
          :aria-label="`Switch to ${theme === 'dark' ? 'light' : 'dark'} theme`"
          @click="toggleTheme"
        >
          <span class="hero-button-icon">
            <component :is="theme === 'dark' ? Sun : Moon" class="h-4.5 w-4.5" />
          </span>
          <span class="min-w-0 flex-1 truncate">{{
            theme === 'dark' ? 'Light mode' : 'Dark mode'
          }}</span>
        </button>
        <div class="flex items-center gap-2 px-3 pt-1" :class="adminPosture.tone">
          <component :is="adminPosture.icon" class="h-3.5 w-3.5 shrink-0" />
          <span class="truncate text-[11px]">{{ adminPosture.label }}</span>
        </div>
      </div>
    </aside>

    <div class="flex min-w-0 flex-1 flex-col">
      <header
        class="glass z-10 flex h-14 shrink-0 items-center justify-between gap-4 border-x-0 border-t-0 px-5"
      >
        <div class="min-w-0">
          <h1 class="truncate text-sm font-semibold tracking-tight text-fg">{{ pageTitle }}</h1>
          <div
            v-if="chip.tone === 'success'"
            class="health-ribbon mt-1.5 max-w-[8rem]"
            aria-hidden="true"
          />
        </div>
        <div class="flex items-center gap-3">
          <div
            class="flex items-center gap-2 rounded-full border border-border px-3 py-1.5 text-xs shadow-[inset_0_1px_0_rgb(255_255_255_/_0.06)]"
            style="background: var(--toolbar-bg)"
          >
            <StatusDot :status="chip.tone" />
            <span class="text-fg-secondary">{{ chip.label }}</span>
            <span v-if="shortRevision" class="font-mono text-fg-muted tnums">{{
              shortRevision
            }}</span>
          </div>
          <span v-if="status" class="font-mono text-xs text-fg-muted tnums"
            >v{{ status.version }}</span
          >
        </div>
      </header>

      <ConfigErrorBanner />

      <main class="min-h-0 flex-1 overflow-y-auto">
        <div class="mx-auto w-full max-w-6xl p-6">
          <RouterView v-slot="{ Component }">
            <Transition name="page" mode="out-in">
              <component :is="Component" :key="authRevision" />
            </Transition>
          </RouterView>
        </div>
      </main>
    </div>
  </div>
</template>
