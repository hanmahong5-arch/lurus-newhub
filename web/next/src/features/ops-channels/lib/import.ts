import type { ImportRequest } from '../types'

export interface ImportForm {
  keys: string
  /** '' = create one new channel per key; otherwise an existing multi-key channel id. */
  target: string
  type: string
  baseUrl: string
  models: string
  group: string
  proxy: string
  probe: boolean
}

export const EMPTY_IMPORT_FORM: ImportForm = {
  keys: '',
  target: '',
  type: '',
  baseUrl: '',
  models: '',
  group: '',
  proxy: '',
  probe: false,
}

export type ImportFormError = 'keys' | 'type'

export type ImportBuild =
  | { ok: true; body: ImportRequest }
  | { ok: false; error: ImportFormError }

/**
 * The two shapes the server accepts: append to channel_id, or create one
 * single-key channel per item (then `type` is required). Blank optional
 * fields are left out so server defaults apply.
 */
export function buildImportRequest(
  form: ImportForm,
  dryRun: boolean
): ImportBuild {
  if (form.keys.trim() === '') return { ok: false, error: 'keys' }
  const body: ImportRequest = {
    dry_run: dryRun,
    probe: !dryRun && form.probe,
    keys: form.keys,
  }
  const proxy = form.proxy.trim()
  if (proxy !== '') body.proxy = proxy

  if (form.target !== '') {
    body.channel_id = Number(form.target)
    return { ok: true, body }
  }
  const type = Number(form.type)
  if (!Number.isInteger(type) || type <= 0) return { ok: false, error: 'type' }
  body.type = type
  const baseUrl = form.baseUrl.trim()
  if (baseUrl !== '') body.base_url = baseUrl
  const models = form.models.trim()
  if (models !== '') body.models = models
  const group = form.group.trim()
  if (group !== '') body.group = group
  return { ok: true, body }
}

/** Probes run serially with a 0.3 to 1.5 s pause; ~0.9 s average plus the call. */
export function estimateProbeSeconds(readyCount: number): number {
  return Math.ceil(readyCount * 1.5)
}
