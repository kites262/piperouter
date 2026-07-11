<template>
  <!--
    table-fixed: columns follow header widths / share free space; long cell
    content truncates instead of inflating min-width.

    overflow-x-auto is intentionally NOT used: absolute tooltips (even when
    opacity:0) expand the scrollable overflow box and produce a phantom
    horizontal scrollbar when the table itself already fits.
  -->
  <div class="card-flat w-full min-w-0 overflow-hidden">
    <table class="w-full table-fixed border-collapse text-sm">
      <slot />
    </table>
  </div>
</template>
