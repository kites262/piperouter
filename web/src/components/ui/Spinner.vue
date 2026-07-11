<script setup lang="ts">
withDefaults(defineProps<{ size?: 'sm' | 'md' | 'lg' }>(), { size: 'md' })

const sizes = { sm: 'h-3.5 w-3.5', md: 'h-5 w-5', lg: 'h-7 w-7' } as const
</script>

<template>
  <svg
    class="animate-spin"
    :class="sizes[size]"
    viewBox="0 0 24 24"
    fill="none"
    role="status"
    aria-label="Loading"
  >
    <circle class="opacity-20" cx="12" cy="12" r="10" stroke="currentColor" stroke-width="3" />
    <path d="M12 2a10 10 0 0 1 10 10" stroke="currentColor" stroke-width="3" stroke-linecap="round" />
  </svg>
</template>
