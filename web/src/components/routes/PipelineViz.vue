<script setup lang="ts">
import { computed } from 'vue'

import type { RouteConfig } from '@/api/types'

/**
 * Horizontal pipeline diagram:
 * Client → Prefix → Rewrite → Transport → Target.
 * Glass nodes + travelling flow dots (hidden under prefers-reduced-motion).
 */
const props = defineProps<{
  route: RouteConfig
  /** Resolved transport type ("direct" | "http" | "socks5") when known. */
  transportType?: string
}>()

const targetURL = computed(() => {
  try {
    return new URL(props.route.target)
  } catch {
    return null
  }
})

interface Stage {
  key: string
  label: string
  value: string
  sub: string
  title: string
  accent?: boolean
}

const stages = computed<Stage[]>(() => {
  const r = props.route
  return [
    { key: 'client', label: 'Client', value: 'HTTP', sub: 'inbound', title: 'Incoming client request' },
    { key: 'prefix', label: 'Prefix', value: r.prefix, sub: 'longest match', title: r.prefix },
    {
      key: 'rewrite',
      label: 'Rewrite',
      value: r.strip_prefix ? 'strip' : 'keep',
      sub: r.strip_prefix ? 'prefix removed' : 'path unchanged',
      title: r.strip_prefix ? 'strip_prefix: true' : 'strip_prefix: false',
    },
    {
      key: 'headers',
      label: 'Headers',
      value: r.strip_forward_headers ? 'strip' : 'keep',
      sub: r.strip_forward_headers ? 'X-Forwarded-* removed' : 'passed through',
      title: r.strip_forward_headers
        ? 'strip_forward_headers: true — Forwarded, Via, X-Forwarded-* removed'
        : 'strip_forward_headers: false — inbound values pass through unchanged',
    },
    {
      key: 'transport',
      label: 'Transport',
      value: r.transport,
      sub: props.transportType ?? '',
      title: r.transport,
    },
    {
      key: 'target',
      label: 'Target',
      value: targetURL.value !== null ? targetURL.value.host : r.target,
      sub: targetURL.value !== null ? targetURL.value.protocol.replace(':', '') : '',
      title: r.target,
      accent: true,
    },
  ]
})
</script>

<template>
  <div class="overflow-x-auto">
    <div class="flex min-w-max items-stretch py-2">
      <template v-for="(stage, i) in stages" :key="stage.key">
        <div
          v-if="i > 0"
          class="pipe relative mx-1.5 w-9 shrink-0 self-center md:w-14"
          aria-hidden="true"
        >
          <span class="flow-dot" :style="{ animationDelay: `${i * 0.25}s` }" />
        </div>

        <div
          class="pipeline-node flex w-32 flex-col justify-center rounded-xl border px-3 py-2.5 md:w-36"
          :class="
            stage.accent
              ? 'border-accent/45 bg-accent-soft/40 shadow-[0_0_24px_-8px_rgb(109_124_255_/_0.45)]'
              : 'border-border'
          "
          :style="stage.accent ? undefined : { background: 'var(--toolbar-bg)' }"
        >
          <span class="text-[10px] font-medium uppercase tracking-widest text-fg-muted">
            {{ stage.label }}
          </span>
          <span class="mt-0.5 truncate font-mono text-sm text-fg" :title="stage.title">
            {{ stage.value }}
          </span>
          <span class="mt-0.5 h-3.5 truncate font-mono text-[10px] text-fg-muted">
            {{ stage.sub }}
          </span>
        </div>
      </template>
    </div>
  </div>
</template>

<style scoped>
.pipe {
  height: 2rem;
}

.pipe::before {
  content: '';
  position: absolute;
  top: 50%;
  left: 0;
  right: 5px;
  height: 1px;
  background: linear-gradient(
    90deg,
    color-mix(in srgb, var(--color-border-strong) 70%, transparent),
    color-mix(in srgb, var(--color-accent) 55%, var(--color-border-strong))
  );
}

.pipe::after {
  content: '';
  position: absolute;
  top: 50%;
  right: 0;
  transform: translateY(-50%);
  border-top: 3px solid transparent;
  border-bottom: 3px solid transparent;
  border-left: 5px solid color-mix(in srgb, var(--color-accent) 70%, var(--color-border-strong));
}

.pipeline-node {
  backdrop-filter: blur(12px) saturate(1.3);
  -webkit-backdrop-filter: blur(12px) saturate(1.3);
  box-shadow: inset 0 1px 0 rgb(255 255 255 / 0.08);
  transition:
    border-color 0.2s ease,
    box-shadow 0.2s ease,
    transform 0.2s var(--ease-out-soft);
}

.pipeline-node:hover {
  transform: translateY(-1px);
  border-color: var(--color-border-strong);
}

.flow-dot {
  position: absolute;
  top: 50%;
  left: 0;
  width: 6px;
  height: 6px;
  margin-top: -3px;
  border-radius: 9999px;
  background: linear-gradient(135deg, var(--color-accent-hover), var(--color-accent));
  box-shadow: 0 0 10px rgb(109 124 255 / 0.9);
  opacity: 0;
  animation: pipe-flow 4.5s linear infinite;
}

@keyframes pipe-flow {
  0%,
  62% {
    left: 0%;
    opacity: 0;
  }
  68% {
    opacity: 1;
  }
  88% {
    opacity: 0.95;
  }
  94%,
  100% {
    left: 100%;
    opacity: 0;
  }
}

@media (prefers-reduced-motion: reduce) {
  .flow-dot {
    display: none;
  }
  .pipeline-node {
    transition: none;
  }
}
</style>
