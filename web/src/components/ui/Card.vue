<script setup lang="ts">
withDefaults(
  defineProps<{
    title?: string
    /**
     * Glass surface for elevated shells (forms, diagnostics panels).
     * Default is the solid surface for dense data regions.
     */
    glass?: boolean
    /** Subtle enter animation for staged page layouts. */
    animate?: boolean
  }>(),
  { title: '', glass: false, animate: false },
)
</script>

<template>
  <section
    class="p-4"
    :class="[
      glass ? 'glass-panel' : 'card-flat',
      animate ? 'animate-fade-scale' : '',
    ]"
  >
    <header v-if="title || $slots.header" class="mb-3 flex items-center justify-between gap-3">
      <h3 v-if="title" class="text-sm font-semibold tracking-tight text-fg">{{ title }}</h3>
      <slot name="header" />
    </header>
    <slot />
  </section>
</template>
