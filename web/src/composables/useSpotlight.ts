import { onBeforeUnmount, onMounted, type Ref } from 'vue'

/**
 * Pointer-follow spotlight for glass cards. Sets CSS vars --spot-x / --spot-y
 * as percentages on the target element. No-ops under coarse pointers or
 * prefers-reduced-motion.
 *
 * Moves are coalesced to one style write per animation frame so rapid
 * pointermove does not thrash layout/style recalculation.
 */
export function useSpotlight(el: Ref<HTMLElement | null>): void {
  let active = false
  let raf = 0
  let nextX = 50
  let nextY = 0

  function enabled(): boolean {
    if (typeof window === 'undefined') return false
    if (window.matchMedia('(prefers-reduced-motion: reduce)').matches) return false
    if (window.matchMedia('(pointer: coarse)').matches) return false
    return true
  }

  function flush(): void {
    raf = 0
    const node = el.value
    if (!node || !active) return
    node.style.setProperty('--spot-x', `${nextX.toFixed(1)}%`)
    node.style.setProperty('--spot-y', `${nextY.toFixed(1)}%`)
  }

  function onMove(event: PointerEvent): void {
    const node = el.value
    if (!node || !active) return
    const rect = node.getBoundingClientRect()
    if (rect.width <= 0 || rect.height <= 0) return
    nextX = ((event.clientX - rect.left) / rect.width) * 100
    nextY = ((event.clientY - rect.top) / rect.height) * 100
    if (raf === 0) raf = requestAnimationFrame(flush)
  }

  function onEnter(): void {
    active = true
  }

  function onLeave(): void {
    active = false
    if (raf !== 0) {
      cancelAnimationFrame(raf)
      raf = 0
    }
  }

  onMounted(() => {
    if (!enabled()) return
    const node = el.value
    if (!node) return
    node.addEventListener('pointerenter', onEnter)
    node.addEventListener('pointerleave', onLeave)
    node.addEventListener('pointermove', onMove, { passive: true })
  })

  onBeforeUnmount(() => {
    if (raf !== 0) cancelAnimationFrame(raf)
    const node = el.value
    if (!node) return
    node.removeEventListener('pointerenter', onEnter)
    node.removeEventListener('pointerleave', onLeave)
    node.removeEventListener('pointermove', onMove)
  })
}
