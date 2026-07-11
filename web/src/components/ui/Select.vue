<script setup lang="ts">
import { ChevronDown } from 'lucide-vue-next'

const model = defineModel<string>({ default: '' })

withDefaults(defineProps<{ disabled?: boolean; invalid?: boolean }>(), {
  disabled: false,
  invalid: false,
})
</script>

<template>
  <div class="relative">
    <select
      v-model="model"
      :disabled="disabled"
      class="h-9 w-full appearance-none rounded-md border bg-bg-deep/70 pl-3 pr-8 text-sm text-fg transition-colors duration-150 focus:outline-none focus:ring-2 disabled:cursor-not-allowed disabled:opacity-50"
      :class="
        invalid
          ? 'border-danger/60 focus:border-danger/70 focus:ring-danger/25'
          : 'border-border focus:border-accent/60 focus:ring-accent/25'
      "
    >
      <slot />
    </select>
    <ChevronDown class="pointer-events-none absolute right-2.5 top-1/2 h-4 w-4 -translate-y-1/2 text-fg-muted" />
  </div>
</template>
