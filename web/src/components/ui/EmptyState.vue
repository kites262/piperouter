<script setup lang="ts">
withDefaults(defineProps<{ title: string; description?: string }>(), { description: '' })
</script>

<template>
  <div
    class="glass-panel flex flex-col items-center justify-center gap-2 px-6 py-14 text-center"
  >
    <div
      v-if="$slots.icon"
      class="logo-glow mb-1 flex h-12 w-12 items-center justify-center rounded-xl border border-accent/25 bg-accent-soft text-accent elevate"
    >
      <slot name="icon" />
    </div>
    <p class="text-sm font-medium tracking-tight text-fg">{{ title }}</p>
    <p v-if="description" class="max-w-sm text-sm text-fg-muted">{{ description }}</p>
    <div v-if="$slots.default" class="mt-3">
      <slot />
    </div>
  </div>
</template>
