<script setup lang="ts">
const model = defineModel<string>({ default: '' })

withDefaults(
  defineProps<{
    type?: string
    placeholder?: string
    disabled?: boolean
    /** Monospace — for paths, URLs, revisions. */
    mono?: boolean
    invalid?: boolean
  }>(),
  { type: 'text', placeholder: '', disabled: false, mono: false, invalid: false },
)
</script>

<template>
  <input
    v-model="model"
    :type="type"
    :placeholder="placeholder"
    :disabled="disabled"
    class="h-9 w-full rounded-md border px-3 text-sm text-fg shadow-[inset_0_1px_0_rgb(255_255_255_/_0.04)] transition-[border-color,box-shadow,background-color] duration-150 placeholder:text-fg-muted focus:outline-none focus:ring-2 disabled:cursor-not-allowed disabled:opacity-50"
    :class="[
      invalid
        ? 'border-danger/60 focus:border-danger/70 focus:ring-danger/25'
        : 'border-border focus:border-accent/55 focus:ring-accent/30 focus:shadow-[0_0_0_3px_rgb(109_124_255_/_0.12)]',
      mono ? 'font-mono' : '',
    ]"
    :style="{ backgroundColor: 'var(--input-bg)' }"
  />
</template>
