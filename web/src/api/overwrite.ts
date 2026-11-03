import type { OverwriteRule } from './types'

/** Read earlier rule formats while all edits use the condition/action format. */
export function normalizeOverwriteRule(rule: OverwriteRule): OverwriteRule {
  const key = rule.field ?? 'model'
  const path = rule.path ?? (/^[A-Za-z_][A-Za-z0-9_]*$/.test(key) ? `.${key}` : `.[${JSON.stringify(key)}]`)
  const phase = rule.phase ?? 'req'
  return {
    name: rule.name,
    enabled: rule.enabled,
    condition: {
      ...(rule.condition ?? { path, op: 'eq', value: rule.from ?? '' }),
      phase: rule.condition?.phase ?? phase,
    },
    action: {
      ...(rule.action ?? { path, op: 'set', value: rule.to ?? '' }),
      phase: rule.action?.phase ?? phase,
    },
  }
}

export function overwritePhaseLabel(phase: string): string {
  if (phase === 'req') return 'Req'
  if (phase === 'res') return 'Res'
  return phase
}

export function overwriteRuleSummary(rule: OverwriteRule): string {
  const { condition, action } = normalizeOverwriteRule(rule)
  return `${overwritePhaseLabel(condition.phase)} ${condition.path} ${condition.op} ${JSON.stringify(condition.value)} → ${overwritePhaseLabel(action.phase)} ${action.path} ${action.op} ${JSON.stringify(action.value)}`
}
