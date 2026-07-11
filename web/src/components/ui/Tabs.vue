<script setup lang="ts">
const model = defineModel<string>({ required: true })

defineProps<{
  tabs: Array<{ value: string; label: string }>
}>()
</script>

<template>
  <div
    class="inline-flex items-center gap-1 rounded-lg border border-border bg-surface p-1"
    role="tablist"
  >
    <button
      v-for="tab in tabs"
      :key="tab.value"
      type="button"
      role="tab"
      :aria-selected="model === tab.value"
      class="rounded-md px-3 py-1.5 text-sm transition-colors duration-150"
      :class="
        model === tab.value
          ? 'bg-accent-soft font-medium text-accent'
          : 'text-fg-secondary hover:bg-surface-raised hover:text-fg'
      "
      @click="model = tab.value"
    >
      {{ tab.label }}
    </button>
  </div>
</template>
