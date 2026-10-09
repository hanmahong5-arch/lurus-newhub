export type JsonCheck =
  | { ok: true; empty: boolean }
  | {
      ok: false
      reason: 'syntax' | 'not-object' | 'header-key' | 'header-value'
    }

const HEADER_NAME = /^[!#$%&'*+.^_`|~0-9A-Za-z-]+$/

/** Client-side mirror of the server's param_override check (object JSON). */
export function checkParamOverride(raw: string): JsonCheck {
  const text = raw.trim()
  if (text === '') return { ok: true, empty: true }
  let parsed: unknown
  try {
    parsed = JSON.parse(text)
  } catch {
    return { ok: false, reason: 'syntax' }
  }
  if (typeof parsed !== 'object' || parsed === null || Array.isArray(parsed)) {
    return { ok: false, reason: 'not-object' }
  }
  return { ok: true, empty: false }
}

/** header_override: a JSON object of valid header names to string values. */
export function checkHeaderOverride(raw: string): JsonCheck {
  const base = checkParamOverride(raw)
  if (!base.ok || base.empty) return base
  const obj = JSON.parse(raw.trim()) as Record<string, unknown>
  for (const [k, v] of Object.entries(obj)) {
    if (!HEADER_NAME.test(k)) return { ok: false, reason: 'header-key' }
    if (typeof v !== 'string') return { ok: false, reason: 'header-value' }
  }
  return base
}

/** Pretty-print valid JSON for the editor; leave anything else untouched. */
export function prettyJson(raw: string): string {
  const text = raw.trim()
  if (text === '') return ''
  try {
    return JSON.stringify(JSON.parse(text), null, 2)
  } catch {
    return raw
  }
}

/** Parse a comma / space separated list of channel ids (null = invalid). */
export function parseChannelIds(raw: string): number[] | null {
  const parts = raw.split(/[\s,]+/).filter((p) => p !== '')
  if (parts.length === 0) return null
  const ids: number[] = []
  for (const p of parts) {
    if (!/^\d+$/.test(p)) return null
    const n = Number(p)
    if (n <= 0) return null
    if (!ids.includes(n)) ids.push(n)
  }
  return ids
}
