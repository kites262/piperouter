<script setup lang="ts">
import Button from './Button.vue'
import Dialog from './Dialog.vue'

const open = defineModel<boolean>('open', { required: true })

withDefaults(
  defineProps<{
    title: string
    message?: string
    confirmText?: string
    cancelText?: string
    /** Destructive action — red confirm button. */
    danger?: boolean
    /** Confirm action in flight. */
    loading?: boolean
  }>(),
  { message: '', confirmText: 'Confirm', cancelText: 'Cancel', danger: false, loading: false },
)

const emit = defineEmits<{ confirm: []; cancel: [] }>()

function onCancel(): void {
  emit('cancel')
  open.value = false
}
</script>

<template>
  <Dialog v-model:open="open" :title="title" max-width="max-w-md">
    <p v-if="message" class="text-sm text-fg-secondary">{{ message }}</p>
    <slot />
    <template #footer>
      <Button variant="ghost" :disabled="loading" @click="onCancel">{{ cancelText }}</Button>
      <Button :variant="danger ? 'danger' : 'default'" :loading="loading" @click="emit('confirm')">
        {{ confirmText }}
      </Button>
    </template>
  </Dialog>
</template>
