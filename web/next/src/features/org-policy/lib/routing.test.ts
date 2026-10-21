import { describe, expect, it } from 'vitest'

import {
  MAX_CANDIDATES,
  MAX_CRITERIA_BYTES,
  MAX_FALLBACK_CANDIDATES,
  MAX_INSTRUCTIONS_BYTES,
  addressable,
  blankCandidate,
  byteLength,
  draftFromWire,
  emptyDraft,
  moveCandidate,
  nextBackupId,
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
      strategy: 'decision',
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

describe('fallback strategy', () => {
  const fb = (over: Partial<RoutingDraft> = {}): RoutingDraft => ({
    ...emptyDraft(PUBLIC),
    strategy: 'fallback',
    fallbackCandidates: [blankCandidate({ id: 'backup-1', model: 'model-b' })],
    ...over,
  })
  const fbCodes = (d: RoutingDraft) => codes(d)

  it('the backup limit mirrors the server (8)', () => {
    expect(MAX_FALLBACK_CANDIDATES).toBe(8)
  })

  it('accepts a single backup with no criteria', () => {
    expect(fbCodes(fb())).toEqual([])
  })

  it.each([
    [0, true],
    [1, false],
    [8, false],
    [9, true],
  ])('backup count %i -> rejected=%s', (n, rejected) => {
    const list = Array.from({ length: n }, (_, i) =>
      blankCandidate({ id: `backup-${i + 1}`, model: `model-${i + 1}` })
    )
    expect(
      fbCodes(fb({ fallbackCandidates: list })).includes('fallback-count')
    ).toBe(rejected)
  })

  it('rejects a backup equal to the public model and an empty model', () => {
    const d = fb({
      fallbackCandidates: [
        blankCandidate({ id: 'backup-1', model: PUBLIC }),
        blankCandidate({ id: 'backup-2', model: '' }),
      ],
    })
    expect(fbCodes(d)).toEqual(
      expect.arrayContaining(['fallback-model-public', 'candidate-model'])
    )
  })

  it('still bounds optional criteria at 2048 bytes', () => {
    const d = fb({
      fallbackCandidates: [
        blankCandidate({
          id: 'backup-1',
          model: 'model-b',
          criteria: 'a'.repeat(2049),
        }),
      ],
    })
    expect(fbCodes(d)).toContain('criteria-too-long')
  })

  it('ignores the decision-only fields entirely', () => {
    const d = fb({
      enabled: true,
      evaluatorModel: '',
      minConfidence: 'junk',
      instructions: 'x'.repeat(5000),
      candidates: [],
    })
    expect(fbCodes(d)).toEqual([])
  })

  it('sends only strategy, enabled and ordered candidates', () => {
    const d = fb({
      enabled: true,
      evaluatorModel: 'model-e',
      instructions: 'leftover',
      defaultCandidate: 'c1',
      fallbackCandidates: [
        blankCandidate({ id: 'backup-1', model: 'model-b', criteria: 'cheap' }),
        blankCandidate({ id: 'backup-2', model: 'model-c' }),
      ],
    })
    expect(toRequestBody(d)).toEqual({
      strategy: 'fallback',
      enabled: true,
      candidates: [
        { id: 'backup-1', model: 'model-b', criteria: 'cheap' },
        { id: 'backup-2', model: 'model-c', criteria: '' },
      ],
    })
  })

  it('maps a stored fallback policy and keeps a decision seed', () => {
    const d = draftFromWire({
      public_model: PUBLIC,
      strategy: 'fallback',
      enabled: true,
      candidates: [
        { id: 'backup-1', model: 'model-b', criteria: '' },
        { id: 'backup-2', model: 'model-c', criteria: '' },
      ],
    })
    expect(d.strategy).toBe('fallback')
    expect(d.fallbackCandidates.map((c) => c.model)).toEqual([
      'model-b',
      'model-c',
    ])
    expect(d.candidates.map((c) => c.model)).toEqual([PUBLIC])
  })

  it('treats a missing or unknown strategy as decision', () => {
    expect(draftFromWire({ candidates: [] }).strategy).toBe('decision')
    expect(draftFromWire({ strategy: 'weird' }).strategy).toBe('decision')
    expect(draftFromWire({ strategy: 'decision', candidates: [{ id: 'c1' }] })
      .fallbackCandidates).toEqual([])
  })

  it('moveCandidate swaps neighbours and clamps at the ends', () => {
    const l = ['a', 'b', 'c']
    expect(moveCandidate(l, 1, -1)).toEqual(['b', 'a', 'c'])
    expect(moveCandidate(l, 1, 1)).toEqual(['a', 'c', 'b'])
    expect(moveCandidate(l, 0, -1)).toEqual(l)
    expect(moveCandidate(l, 2, 1)).toEqual(l)
    expect(l).toEqual(['a', 'b', 'c'])
  })

  it('nextBackupId never reuses an id in the list', () => {
    expect(nextBackupId([])).toBe('backup-1')
    const list = [
      blankCandidate({ id: 'backup-1' }),
      blankCandidate({ id: 'backup-3' }),
    ]
    expect(nextBackupId(list)).toBe('backup-2')
  })
})
