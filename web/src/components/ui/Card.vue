<script setup lang="ts">
withDefaults(
  defineProps<{
    title?: string
    /**
     * Glass surface — reserve for dashboard stat cards (PRD §18.3).
     * Default is the flat solid surface for dense data.
     */
    glass?: boolean
  }>(),
  { title: '', glass: false },
)
</script>

<template>
  <section class="p-4" :class="glass ? 'glass rounded-xl' : 'card-flat'">
    <header v-if="title || $slots.header" class="mb-3 flex items-center justify-between gap-3">
      <h3 v-if="title" class="text-sm font-semibold text-fg">{{ title }}</h3>
      <slot name="header" />
    </header>
    <slot />
  </section>
</template>
