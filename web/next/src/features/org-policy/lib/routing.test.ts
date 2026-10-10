import { describe, expect, it } from 'vitest'

import {
  MAX_CANDIDATES,
  MAX_CRITERIA_BYTES,
  MAX_INSTRUCTIONS_BYTES,
  addressable,
  blankCandidate,
  byteLength,
  draftFromWire,
  emptyDraft,
  parseConfidence,
  toRequestBody,
  validateDraft,
  type RoutingDraft,
} from './routing'

const PUBLIC = 'model-a'

function valid(over: Partial<RoutingDraft> = {}): RoutingDraft {
  return {
    ...emptyDraft(PUBLIC),
    evaluatorModel: 'model-e',
    candidates: [
      blankCandidate({ id: 'c1', model: PUBLIC, criteria: 'simple questions' }),
    ],
    ...over,
  }
}

function candidates(n: number) {
  return Array.from({ length: n }, (_, i) =>
    blankCandidate({
      id: `c${i}`,
      model: i === 0 ? PUBLIC : `model-${i}`,
      criteria: 'x',
    })
  )
}

const codes = (d: RoutingDraft) => validateDraft(d, PUBLIC).map((e) => e.code)

describe('validateDraft', () => {
  it('accepts a minimal valid policy', () => {
    expect(codes(valid())).toEqual([])
  })

  it.each([
    [0, true],
    [1, false],
    [32, false],
    [33, true],
  ])('candidate count %i -> rejected=%s', (n, rejected) => {
    const errs = codes(valid({ candidates: candidates(n) }))
    expect(errs.includes('candidates-count')).toBe(rejected)
  })

  it('the candidate limit mirrors the server (32)', () => {
    expect(MAX_CANDIDATES).toBe(32)
  })

  it.each([
    [2048, false],
    [2049, true],
  ])('criteria of %i bytes -> rejected=%s', (n, rejected) => {
    const d = valid({
      candidates: [blankCandidate({ id: 'c1', model: PUBLIC, criteria: 'a'.repeat(n) })],
    })
    expect(codes(d).includes('criteria-too-long')).toBe(rejected)
    expect(MAX_CRITERIA_BYTES).toBe(2048)
  })

  it('counts bytes, not characters', () => {
    // 683 three-byte characters = 2049 bytes but only 683 characters.
    const criteria = '中'.repeat(683)
    expect(criteria.length).toBeLessThan(MAX_CRITERIA_BYTES)
    expect(byteLength(criteria)).toBe(2049)
    const d = valid({ candidates: [blankCandidate({ id: 'c1', model: PUBLIC, criteria })] })
    expect(codes(d)).toContain('criteria-too-long')
  })

  it.each([
    [4096, false],
    [4097, true],
  ])('instructions of %i bytes -> rejected=%s', (n, rejected) => {
    const errs = codes(valid({ instructions: 'a'.repeat(n) }))
    expect(errs.includes('instructions-too-long')).toBe(rejected)
    expect(MAX_INSTRUCTIONS_BYTES).toBe(4096)
  })

  it.each([
    ['0', false],
    ['1', false],
    ['0.65', false],
    ['-0.1', true],
    ['1.01', true],
    ['abc', true],
    ['', true],
  ])('confidence %j -> rejected=%s', (text, rejected) => {
    expect(
      codes(valid({ minConfidence: text })).includes('min-confidence')
    ).toBe(rejected)
  })

  it('requires an evaluator only when enabling', () => {
    expect(codes(valid({ enabled: false, evaluatorModel: '' }))).toEqual([])
    expect(codes(valid({ enabled: true, evaluatorModel: ' ' }))).toContain(
      'evaluator-required'
    )
  })

  it('rejects bad, reserved and duplicate candidate ids', () => {
    const d = valid({
      candidates: [
        blankCandidate({ id: 'bad id', model: PUBLIC, criteria: 'x' }),
        blankCandidate({ id: 'no_preference', model: 'm', criteria: 'x' }),
        blankCandidate({ id: 'dup', model: 'm', criteria: 'x' }),
        blankCandidate({ id: 'dup', model: 'm', criteria: 'x' }),
      ],
    })
    expect(codes(d)).toEqual(
      expect.arrayContaining([
        'candidate-id',
        'candidate-id-reserved',
        'candidate-id-duplicate',
      ])
    )
  })

  it('needs a default when the public model is not itself a candidate', () => {
    const d = valid({
      candidates: [blankCandidate({ id: 'c1', model: 'model-b', criteria: 'x' })],
    })
    expect(codes(d)).toContain('default-required')
    expect(codes({ ...d, defaultCandidate: 'c1' })).toEqual([])
    expect(codes({ ...d, defaultCandidate: 'nope' })).toContain(
      'default-unknown'
    )
  })
})

describe('wire mapping', () => {
  it('round-trips a server policy into the request body', () => {
    const d = draftFromWire({
      enabled: true,
      evaluator_model: 'model-e',
      instructions: 'be brief',
      min_confidence: 0.8,
      default_candidate: 'c1',
      candidates: [{ id: 'c1', model: 'model-b', criteria: 'x' }],
    })
    expect(toRequestBody(d)).toEqual({
      enabled: true,
      evaluator_model: 'model-e',
      instructions: 'be brief',
      min_confidence: 0.8,
      default_candidate: 'c1',
      candidates: [{ id: 'c1', model: 'model-b', criteria: 'x' }],
    })
  })

  it('degrades malformed answers to an empty draft, never throws', () => {
    expect(draftFromWire(null).candidates).toEqual([])
    expect(draftFromWire({ min_confidence: 'x' }).minConfidence).toBe('0.65')
  })

  it('parseConfidence keeps 0 distinct from blank', () => {
    expect(parseConfidence('0')).toBe(0)
    expect(parseConfidence(' ')).toBeNull()
  })

  it('only single-segment model names are addressable', () => {
    expect(addressable('model-a')).toBe(true)
    expect(addressable('vendor/model-a')).toBe(false)
  })
})
