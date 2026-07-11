<script setup lang="ts">
defineProps<{ text: string }>()
</script>

<template>
  <span class="group relative inline-flex">
    <slot />
    <span
      class="pointer-events-none absolute bottom-full left-1/2 z-50 mb-1.5 -translate-x-1/2 whitespace-nowrap rounded-md border border-border bg-surface-raised px-2 py-1 text-xs text-fg opacity-0 shadow-lg shadow-black/30 transition-opacity duration-150 group-hover:opacity-100 group-focus-within:opacity-100"
      role="tooltip"
    >
      {{ text }}
    </span>
  </span>
</template>
