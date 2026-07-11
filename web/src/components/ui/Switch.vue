<script setup lang="ts">
const model = defineModel<boolean>({ default: false })

withDefaults(defineProps<{ disabled?: boolean }>(), { disabled: false })
</script>

<template>
  <button
    type="button"
    role="switch"
    :aria-checked="model"
    :disabled="disabled"
    class="relative inline-flex h-5 w-9 shrink-0 items-center rounded-full border border-border transition-[background-color,box-shadow] duration-200 ease-spring focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent disabled:cursor-not-allowed disabled:opacity-50"
    :class="
      model
        ? 'bg-accent shadow-[0_0_12px_-2px_rgb(109_124_255_/_0.55)]'
        : 'bg-surface-raised'
    "
    @click="model = !model"
  >
    <span
      class="inline-block h-3.5 w-3.5 rounded-full bg-white shadow-sm transition-transform duration-200 ease-spring"
      :class="model ? 'translate-x-[18px]' : 'translate-x-[3px]'"
    />
  </button>
</template>
