<template>
  <!--
    Do NOT put position:relative / pseudo-elements on <tr> — that breaks
    table column alignment in Chromium/WebKit. Hover + accent live on <td>.
  -->
  <tr class="group/row transition-colors duration-150 hover:bg-surface-raised/55">
    <slot />
  </tr>
</template>
