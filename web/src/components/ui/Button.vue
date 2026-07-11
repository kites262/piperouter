<script setup lang="ts">
import { computed } from 'vue'

import Spinner from './Spinner.vue'

const props = withDefaults(
  defineProps<{
    variant?: 'default' | 'outline' | 'ghost' | 'danger'
    size?: 'sm' | 'md' | 'lg' | 'icon'
    type?: 'button' | 'submit' | 'reset'
    disabled?: boolean
    loading?: boolean
  }>(),
  { variant: 'default', size: 'md', type: 'button', disabled: false, loading: false },
)

const variants = {
  // Accent button carries a faint top-down sheen + elevation for a machined,
  // pressable feel; the base class adds the press (active) micro-interaction.
  default:
    'bg-accent text-white hover:bg-accent-hover elevate [background-image:linear-gradient(180deg,rgb(255_255_255_/_0.14),transparent_55%)]',
  outline: 'border border-border-strong bg-surface/40 text-fg hover:border-border hover:bg-surface-raised',
  ghost: 'text-fg-secondary hover:bg-surface-raised hover:text-fg',
  danger: 'border border-danger/30 bg-danger/15 text-danger hover:bg-danger/25',
} as const

const sizes = {
  sm: 'h-7 gap-1 px-2.5 text-xs',
  md: 'h-9 gap-1.5 px-3.5 text-sm',
  lg: 'h-10 gap-2 px-5 text-sm',
  icon: 'h-9 w-9 gap-0',
} as const

const classes = computed(() => [variants[props.variant], sizes[props.size]])
</script>

<template>
  <button
    :type="type"
    :disabled="disabled || loading"
    class="inline-flex select-none items-center justify-center whitespace-nowrap rounded-md font-medium transition-[color,background-color,border-color,box-shadow,transform] duration-150 focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent active:translate-y-px disabled:pointer-events-none disabled:opacity-50"
    :class="classes"
  >
    <Spinner v-if="loading" size="sm" />
    <slot />
  </button>
</template>
