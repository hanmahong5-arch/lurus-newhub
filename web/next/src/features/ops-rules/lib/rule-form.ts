import type { ContentRule, RuleWriteBody } from '../types'

export interface RuleFormValues {
  name: string
  ordinal: string
  role_scope: string
  kind: string
  pattern_type: string
  builtin: string
  pattern: string
  replacement: string
  mode: string
  enabled: boolean
}

export type RuleFormError = 'name' | 'ordinal' | 'builtin' | 'pattern'

export const EMPTY_RULE_FORM: RuleFormValues = {
  name: '',
  ordinal: '0',
  role_scope: 'any',
  kind: 'mask',
  pattern_type: 'builtin',
  builtin: '',
  pattern: '',
  replacement: '',
  mode: 'observe',
  enabled: true,
}

export function formFromRule(rule: ContentRule): RuleFormValues {
  return {
    name: rule.name,
    ordinal: String(rule.ordinal),
    role_scope: rule.role_scope,
    kind: rule.kind,
    pattern_type: rule.pattern_type,
    builtin: rule.builtin,
    pattern: rule.pattern,
    replacement: rule.replacement,
    mode: rule.mode,
    enabled: rule.enabled,
  }
}

/** The PUT handler overwrites every field, so a partial edit sends it all. */
export function bodyFromRule(
  rule: ContentRule,
  patch: Partial<RuleWriteBody> = {}
): RuleWriteBody {
  return {
    ordinal: rule.ordinal,
    name: rule.name,
    role_scope: rule.role_scope,
    kind: rule.kind,
    pattern_type: rule.pattern_type,
    builtin: rule.builtin,
    pattern: rule.pattern,
    replacement: rule.replacement,
    mode: rule.mode,
    enabled: rule.enabled,
    ...patch,
  }
}

export function buildRuleBody(
  f: RuleFormValues
): { ok: true; body: RuleWriteBody } | { ok: false; error: RuleFormError } {
  const name = f.name.trim()
  if (name === '') return { ok: false, error: 'name' }
  const ordinal = Number(f.ordinal.trim() === '' ? '0' : f.ordinal)
  if (!Number.isInteger(ordinal) || ordinal < 0) {
    return { ok: false, error: 'ordinal' }
  }
  const builtin = f.pattern_type === 'builtin'
  if (builtin && f.builtin === '') return { ok: false, error: 'builtin' }
  if (!builtin && f.pattern === '') return { ok: false, error: 'pattern' }
  return {
    ok: true,
    body: {
      ordinal,
      name,
      role_scope: f.role_scope,
      kind: f.kind,
      pattern_type: f.pattern_type,
      // The server refuses a builtin rule with a pattern and vice versa.
      builtin: builtin ? f.builtin : '',
      pattern: builtin ? '' : f.pattern,
      replacement: f.kind === 'mask' ? f.replacement : '',
      mode: f.mode,
      enabled: f.enabled,
    },
  }
}
