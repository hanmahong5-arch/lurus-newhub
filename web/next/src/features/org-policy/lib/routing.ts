/**
 * Decision-routing policy: wire shape, draft model and validation.
 * Source of truth (Go): v2_routing_policy.go and app/routingdecision/policy.go.
 * The front end mirrors the server's rules so a bad policy is stopped before
 * the request; the server stays the authority.
 */

export const MAX_CANDIDATES = 32
export const MAX_CRITERIA_BYTES = 2048
export const MAX_INSTRUCTIONS_BYTES = 4096
export const MAX_CANDIDATE_ID_LEN = 64
export const MAX_MODEL_NAME_LEN = 128
export const NO_PREFERENCE_ID = 'no_preference'
export const DEFAULT_MIN_CONFIDENCE = 0.65

const CANDIDATE_ID_RE = /^[A-Za-z0-9][A-Za-z0-9_.-]*$/

export interface RoutingCandidate {
  id: string
  model: string
  criteria: string
}

/** A candidate in the editor: `uid` is a client-only React key, never sent. */
export interface DraftCandidate extends RoutingCandidate {
  uid: number
}

let uidSeq = 0
export function blankCandidate(over: Partial<RoutingCandidate> = {}): DraftCandidate {
  uidSeq += 1
  return { uid: uidSeq, id: '', model: '', criteria: '', ...over }
}

export interface RoutingDraft {
  enabled: boolean
  evaluatorModel: string
  instructions: string
  /** Kept as text so a half-typed number is not lost; parsed on validate. */
  minConfidence: string
  defaultCandidate: string
  candidates: DraftCandidate[]
}

export type RoutingError =
  | { code: 'candidates-count' }
  | { code: 'min-confidence' }
  | { code: 'instructions-too-long' }
  | { code: 'evaluator-too-long' }
  | { code: 'evaluator-required' }
  | { code: 'candidate-id'; index: number }
  | { code: 'candidate-id-reserved'; index: number }
  | { code: 'candidate-id-duplicate'; index: number }
  | { code: 'candidate-model'; index: number }
  | { code: 'criteria-required'; index: number }
  | { code: 'criteria-too-long'; index: number }
  | { code: 'default-unknown' }
  | { code: 'default-required' }

/** UTF-8 byte length: the server's limits are in bytes, not characters. */
export function byteLength(s: string): number {
  return new TextEncoder().encode(s).length
}

export function emptyDraft(publicModel: string): RoutingDraft {
  return {
    enabled: false,
    evaluatorModel: '',
    instructions: '',
    minConfidence: String(DEFAULT_MIN_CONFIDENCE),
    defaultCandidate: '',
    candidates: [blankCandidate({ model: publicModel })],
  }
}

function str(v: unknown): string {
  return typeof v === 'string' ? v : ''
}

/** GET/PUT answer -> draft. Anything malformed degrades to empty fields. */
export function draftFromWire(raw: unknown): RoutingDraft {
  const r =
    typeof raw === 'object' && raw !== null
      ? (raw as Record<string, unknown>)
      : {}
  const cands = Array.isArray(r.candidates) ? r.candidates : []
  const mc =
    typeof r.min_confidence === 'number' && Number.isFinite(r.min_confidence)
      ? r.min_confidence
      : DEFAULT_MIN_CONFIDENCE
  return {
    enabled: r.enabled === true,
    evaluatorModel: str(r.evaluator_model),
    instructions: str(r.instructions),
    minConfidence: String(mc),
    defaultCandidate: str(r.default_candidate),
    candidates: cands.map((c) => {
      const x =
        typeof c === 'object' && c !== null
          ? (c as Record<string, unknown>)
          : {}
      return blankCandidate({
        id: str(x.id),
        model: str(x.model),
        criteria: str(x.criteria),
      })
    }),
  }
}

/** Parse the confidence text; null when it is not a finite number in [0, 1]. */
export function parseConfidence(text: string): number | null {
  if (text.trim() === '') return null
  const n = Number(text)
  return Number.isFinite(n) && n >= 0 && n <= 1 ? n : null
}

export function validateDraft(
  d: RoutingDraft,
  publicModel: string
): RoutingError[] {
  const errs: RoutingError[] = []
  if (parseConfidence(d.minConfidence) === null) {
    errs.push({ code: 'min-confidence' })
  }
  if (byteLength(d.instructions) > MAX_INSTRUCTIONS_BYTES) {
    errs.push({ code: 'instructions-too-long' })
  }
  if (byteLength(d.evaluatorModel) > MAX_MODEL_NAME_LEN) {
    errs.push({ code: 'evaluator-too-long' })
  }
  if (d.enabled && d.evaluatorModel.trim() === '') {
    errs.push({ code: 'evaluator-required' })
  }
  const n = d.candidates.length
  if (n < 1 || n > MAX_CANDIDATES) errs.push({ code: 'candidates-count' })

  const seen = new Set<string>()
  let defaultOk = false
  let publicIsCandidate = false
  d.candidates.forEach((c, index) => {
    if (
      c.id === '' ||
      c.id.length > MAX_CANDIDATE_ID_LEN ||
      !CANDIDATE_ID_RE.test(c.id)
    ) {
      errs.push({ code: 'candidate-id', index })
    } else if (c.id === NO_PREFERENCE_ID) {
      errs.push({ code: 'candidate-id-reserved', index })
    } else if (seen.has(c.id)) {
      errs.push({ code: 'candidate-id-duplicate', index })
    }
    seen.add(c.id)
    if (c.model === '' || byteLength(c.model) > MAX_MODEL_NAME_LEN) {
      errs.push({ code: 'candidate-model', index })
    }
    if (c.criteria.trim() === '') {
      errs.push({ code: 'criteria-required', index })
    } else if (byteLength(c.criteria) > MAX_CRITERIA_BYTES) {
      errs.push({ code: 'criteria-too-long', index })
    }
    if (c.id === d.defaultCandidate) defaultOk = true
    if (c.model === publicModel) publicIsCandidate = true
  })
  if (d.defaultCandidate !== '' && !defaultOk) {
    errs.push({ code: 'default-unknown' })
  }
  if (d.defaultCandidate === '' && !publicIsCandidate) {
    errs.push({ code: 'default-required' })
  }
  return errs
}

/** Request body of PUT /routing-policies/:model. Call only after validateDraft is clean. */
export function toRequestBody(d: RoutingDraft) {
  return {
    enabled: d.enabled,
    evaluator_model: d.evaluatorModel.trim(),
    instructions: d.instructions,
    min_confidence: parseConfidence(d.minConfidence) ?? DEFAULT_MIN_CONFIDENCE,
    default_candidate: d.defaultCandidate,
    candidates: d.candidates.map((c) => ({
      id: c.id,
      model: c.model,
      criteria: c.criteria,
    })),
  }
}

/** Models whose name has "/" cannot be addressed as one path segment. */
export function addressable(model: string): boolean {
  return model !== '' && !model.includes('/')
}
