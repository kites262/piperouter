import { onBeforeUnmount, onMounted, type Ref } from 'vue'

/**
 * Pointer-follow spotlight for glass cards. Sets CSS vars --spot-x / --spot-y
 * as percentages on the target element. No-ops under coarse pointers or
 * prefers-reduced-motion.
 */
export function useSpotlight(el: Ref<HTMLElement | null>): void {
  let active = false

  function enabled(): boolean {
    if (typeof window === 'undefined') return false
    if (window.matchMedia('(prefers-reduced-motion: reduce)').matches) return false
    if (window.matchMedia('(pointer: coarse)').matches) return false
    return true
  }

  function onMove(event: PointerEvent): void {
    const node = el.value
    if (!node || !active) return
    const rect = node.getBoundingClientRect()
    if (rect.width <= 0 || rect.height <= 0) return
    const x = ((event.clientX - rect.left) / rect.width) * 100
    const y = ((event.clientY - rect.top) / rect.height) * 100
    node.style.setProperty('--spot-x', `${x.toFixed(2)}%`)
    node.style.setProperty('--spot-y', `${y.toFixed(2)}%`)
  }

  function onEnter(): void {
    active = true
  }

  function onLeave(): void {
    active = false
  }

  onMounted(() => {
    if (!enabled()) return
    const node = el.value
    if (!node) return
    node.addEventListener('pointerenter', onEnter)
    node.addEventListener('pointerleave', onLeave)
    node.addEventListener('pointermove', onMove)
  })

  onBeforeUnmount(() => {
    const node = el.value
    if (!node) return
    node.removeEventListener('pointerenter', onEnter)
    node.removeEventListener('pointerleave', onLeave)
    node.removeEventListener('pointermove', onMove)
  })
}
