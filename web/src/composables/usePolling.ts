import { onBeforeUnmount, onMounted, readonly, ref, type Ref } from 'vue'

export interface UsePollingOptions {
  /** Poll interval in milliseconds. Default 5000. */
  interval?: number
  /** Run the callback immediately on mount. Default true. */
  immediate?: boolean
  /** Stop polling while the document is hidden; refresh on return. Default true. */
  pauseWhenHidden?: boolean
}

export interface UsePolling {
  /** Manually pause polling (stays paused across visibility changes). */
  pause: () => void
  /** Resume polling; triggers an immediate refresh. */
  resume: () => void
  /** Run the callback once right now (deduplicates overlapping calls). */
  refresh: () => Promise<void>
  /** True while manually paused via pause(). */
  isPaused: Readonly<Ref<boolean>>
}

/**
 * setInterval-based polling tied to the component lifecycle.
 *
 * - first call fires immediately on mount (unless immediate: false)
 * - pauses while the tab is hidden, refreshes as soon as it is visible again
 * - overlapping async invocations are skipped, never queued
 */
export function usePolling(
  callback: () => unknown | Promise<unknown>,
  options: UsePollingOptions = {},
): UsePolling {
  const { interval = 5000, immediate = true, pauseWhenHidden = true } = options

  const isPaused = ref(false)
  let timer: number | null = null
  let inFlight = false

  async function refresh(): Promise<void> {
    if (inFlight) return
    inFlight = true
    try {
      await callback()
    } finally {
      inFlight = false
    }
  }

  function startTimer(): void {
    if (timer !== null) return
    timer = window.setInterval(() => {
      if (pauseWhenHidden && document.hidden) return
      void refresh()
    }, interval)
  }

  function stopTimer(): void {
    if (timer !== null) {
      window.clearInterval(timer)
      timer = null
    }
  }

  function pause(): void {
    isPaused.value = true
    stopTimer()
  }

  function resume(): void {
    if (!isPaused.value && timer !== null) return
    isPaused.value = false
    void refresh()
    startTimer()
  }

  function onVisibilityChange(): void {
    if (!pauseWhenHidden || isPaused.value) return
    if (document.hidden) {
      stopTimer()
    } else {
      void refresh()
      startTimer()
    }
  }

  onMounted(() => {
    if (immediate) void refresh()
    startTimer()
    document.addEventListener('visibilitychange', onVisibilityChange)
  })

  onBeforeUnmount(() => {
    stopTimer()
    document.removeEventListener('visibilitychange', onVisibilityChange)
  })

  return { pause, resume, refresh, isPaused: readonly(isPaused) }
}
