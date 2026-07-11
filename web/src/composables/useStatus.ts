import type { InjectionKey, Ref } from 'vue'

import type { StatusResponse } from '@/api/types'

/**
 * Shared server-status state. AppShell polls GET /api/v1/status every 5s and
 * provides this under statusKey so descendants (top-bar chip,
 * ConfigErrorBanner, pages) reuse a single poll loop.
 */
export interface StatusState {
  status: Ref<StatusResponse | null>
  /** Human-readable fetch error ("" ↔ null when the API is reachable). */
  error: Ref<string | null>
  /** Re-poll immediately. */
  refresh: () => Promise<void>
}

export const statusKey: InjectionKey<StatusState> = Symbol('piperouter-status')
