<template>
  <!--
    Default: allow shrink (min-w-0). Callers that need a fixed mono column
    add whitespace-nowrap / font-mono themselves. First cell gets a hover
    accent bar via box-shadow so <tr> layout stays intact.
  -->
  <td
    class="min-w-0 px-3 py-2.5 align-middle text-fg-secondary first:shadow-[inset_2px_0_0_0_transparent] group-hover/row:first:shadow-[inset_2px_0_0_0_var(--color-accent)]"
  >
    <slot />
  </td>
</template>
