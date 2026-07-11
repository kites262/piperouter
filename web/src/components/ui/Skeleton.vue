<!-- Shimmer skeleton placeholder. Size it with utility classes on the tag,
     e.g. <Skeleton class="h-4 w-32" />. The sweep is theme-aware and is
     frozen (static bar) under prefers-reduced-motion via the global rule. -->
<script setup lang="ts">
withDefaults(defineProps<{ rounded?: string }>(), { rounded: 'rounded-md' })
</script>

<template>
  <span
    class="block animate-shimmer bg-surface-raised"
    :class="rounded"
    style="
      background-image: linear-gradient(90deg, transparent, var(--skeleton-sheen), transparent);
      background-size: 160% 100%;
      background-repeat: no-repeat;
    "
    aria-hidden="true"
  />
</template>
