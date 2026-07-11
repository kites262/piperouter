<script setup lang="ts">
withDefaults(defineProps<{ title: string; description?: string }>(), { description: '' })
</script>

<template>
  <div class="card-flat flex flex-col items-center justify-center gap-2 px-6 py-14 text-center">
    <div
      v-if="$slots.icon"
      class="mb-1 flex h-10 w-10 items-center justify-center rounded-lg bg-surface-raised text-fg-muted"
    >
      <slot name="icon" />
    </div>
    <p class="text-sm font-medium text-fg">{{ title }}</p>
    <p v-if="description" class="max-w-sm text-sm text-fg-muted">{{ description }}</p>
    <div v-if="$slots.default" class="mt-3">
      <slot />
    </div>
  </div>
</template>
