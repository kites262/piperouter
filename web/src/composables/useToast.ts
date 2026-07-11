import { reactive } from 'vue'

export type ToastVariant = 'info' | 'success' | 'warning' | 'error'

export interface Toast {
  id: number
  variant: ToastVariant
  title: string
  message?: string
}

export interface ToastOptions {
  /** Secondary line under the title. */
  message?: string
  /** Auto-dismiss delay in ms; 0 keeps the toast until dismissed. */
  duration?: number
}

// Module-level store — every useToast() consumer shares the same list.
const toasts = reactive<Toast[]>([])
const timers = new Map<number, number>()
let seq = 0

function dismiss(id: number): void {
  const index = toasts.findIndex((t) => t.id === id)
  if (index !== -1) toasts.splice(index, 1)
  const timer = timers.get(id)
  if (timer !== undefined) {
    window.clearTimeout(timer)
    timers.delete(id)
  }
}

function push(variant: ToastVariant, title: string, options: ToastOptions = {}): number {
  const id = ++seq
  toasts.push({ id, variant, title, message: options.message })
  const duration = options.duration ?? (variant === 'error' ? 6000 : 4000)
  if (duration > 0) {
    timers.set(
      id,
      window.setTimeout(() => dismiss(id), duration),
    )
  }
  return id
}

export function useToast() {
  return {
    /** Reactive toast list (rendered by <Toaster>). */
    toasts,
    dismiss,
    toast: push,
    info: (title: string, options?: ToastOptions) => push('info', title, options),
    success: (title: string, options?: ToastOptions) => push('success', title, options),
    warning: (title: string, options?: ToastOptions) => push('warning', title, options),
    error: (title: string, options?: ToastOptions) => push('error', title, options),
  }
}
