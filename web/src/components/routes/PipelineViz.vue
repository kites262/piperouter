<script setup lang="ts">
import { computed } from 'vue'

import type { RouteConfig } from '@/api/types'

/**
 * Horizontal flat pipeline diagram (PRD §19.4):
 * Client → Prefix → Rewrite → Transport → Target.
 * Simple boxes + arrowed connectors, with a low-frequency "flow dot"
 * travelling along each pipe (hidden under prefers-reduced-motion).
 * No drag, no DAG.
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
    },
  ]
})
</script>

<template>
  <div class="overflow-x-auto">
    <div class="flex min-w-max items-stretch py-1">
      <template v-for="(stage, i) in stages" :key="stage.key">
        <!-- Connector pipe with arrowhead + travelling flow dot -->
        <div v-if="i > 0" class="pipe relative mx-1.5 w-9 shrink-0 self-center md:w-14" aria-hidden="true">
          <span class="flow-dot" :style="{ animationDelay: `${i * 0.25}s` }" />
        </div>

        <div
          class="flex w-32 flex-col justify-center rounded-lg border bg-surface px-3 py-2.5 md:w-36"
          :class="stage.key === 'target' ? 'border-accent/40' : 'border-border'"
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

/* Pipe line */
.pipe::before {
  content: '';
  position: absolute;
  top: 50%;
  left: 0;
  right: 5px;
  height: 1px;
  background-color: var(--color-border-strong);
}

/* Arrowhead */
.pipe::after {
  content: '';
  position: absolute;
  top: 50%;
  right: 0;
  transform: translateY(-50%);
  border-top: 3px solid transparent;
  border-bottom: 3px solid transparent;
  border-left: 5px solid var(--color-border-strong);
}

/*
 * Low-frequency flow dot: idles for most of the cycle, then glides
 * left → right once every ~5s (PRD §18.4 "Pipeline 上的低频流动点").
 */
.flow-dot {
  position: absolute;
  top: 50%;
  left: 0;
  width: 5px;
  height: 5px;
  margin-top: -2.5px;
  border-radius: 9999px;
  background-color: var(--color-accent);
  box-shadow: 0 0 6px rgb(109 124 255 / 0.8);
  opacity: 0;
  animation: pipe-flow 5s linear infinite;
}

@keyframes pipe-flow {
  0%,
  70% {
    left: 0%;
    opacity: 0;
  }
  74% {
    opacity: 0.9;
  }
  86% {
    opacity: 0.9;
  }
  90%,
  100% {
    left: 100%;
    opacity: 0;
  }
}

@media (prefers-reduced-motion: reduce) {
  .flow-dot {
    display: none;
  }
}
</style>
