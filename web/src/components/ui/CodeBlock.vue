<script setup lang="ts">
withDefaults(defineProps<{ code: string; label?: string }>(), { label: '' })
</script>

<template>
  <div class="card-flat overflow-hidden">
    <div v-if="label" class="border-b border-border px-3 py-1.5 font-mono text-xs text-fg-muted">
      {{ label }}
    </div>
    <pre class="overflow-x-auto p-3 font-mono text-xs leading-relaxed text-fg-secondary">{{ code }}</pre>
  </div>
</template>
