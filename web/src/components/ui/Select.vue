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
      class="h-9 w-full appearance-none rounded-md border pl-3 pr-8 text-sm text-fg shadow-[inset_0_1px_0_rgb(255_255_255_/_0.04)] transition-[border-color,box-shadow,background-color] duration-150 focus:outline-none focus:ring-2 disabled:cursor-not-allowed disabled:opacity-50"
      :class="
        invalid
          ? 'border-danger/60 focus:border-danger/70 focus:ring-danger/25'
          : 'border-border focus:border-accent/55 focus:ring-accent/30 focus:shadow-[0_0_0_3px_rgb(109_124_255_/_0.12)]'
      "
      :style="{ backgroundColor: 'var(--input-bg)' }"
    >
      <slot />
    </select>
    <ChevronDown
      class="pointer-events-none absolute right-2.5 top-1/2 h-4 w-4 -translate-y-1/2 text-fg-muted"
    />
  </div>
</template>
