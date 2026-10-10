import type { KeySettingsBody } from '../api'

export const DEFAULT_KEY_WEIGHT = 50
export const MAX_KEY_WEIGHT = 100

export interface KeyForm {
  weight: string
  proxy: string
}

export type KeyFormError = 'weight'

export type KeyFormBuild =
  | { ok: true; body: KeySettingsBody }
  | { ok: false; error: KeyFormError }

/** Only fields that differ from the current values are sent. */
export function buildKeySettings(
  form: KeyForm,
  current: { weight: number; proxy: string }
): KeyFormBuild {
  const body: KeySettingsBody = {}
  const w = form.weight.trim()
  if (w !== '') {
    const n = Number(w)
    if (!Number.isInteger(n) || n < 0 || n > MAX_KEY_WEIGHT) {
      return { ok: false, error: 'weight' }
    }
    if (n !== current.weight) body.weight = n
  }
  const proxy = form.proxy.trim()
  if (proxy !== current.proxy) body.proxy = proxy
  return { ok: true, body }
}
