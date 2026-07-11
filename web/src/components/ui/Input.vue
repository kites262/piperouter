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
    class="h-9 w-full rounded-md border bg-bg-deep/70 px-3 text-sm text-fg transition-colors duration-150 placeholder:text-fg-muted focus:outline-none focus:ring-2 disabled:cursor-not-allowed disabled:opacity-50"
    :class="[
      invalid
        ? 'border-danger/60 focus:border-danger/70 focus:ring-danger/25'
        : 'border-border focus:border-accent/60 focus:ring-accent/25',
      mono ? 'font-mono' : '',
    ]"
  />
</template>
