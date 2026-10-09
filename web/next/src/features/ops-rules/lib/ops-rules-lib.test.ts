import { describe, expect, it } from 'vitest'

import {
  checkHeaderOverride,
  checkParamOverride,
  parseChannelIds,
  prettyJson,
} from './json'
import { mapApplyOutcome, mapRuleList, mapTemplateList } from './map'
import { EMPTY_RULE_FORM, buildRuleBody } from './rule-form'

describe('json checks', () => {
  it('accepts empty and objects, rejects syntax errors and non-objects', () => {
    expect(checkParamOverride('  ')).toEqual({ ok: true, empty: true })
    expect(checkParamOverride('{"a":1}')).toEqual({ ok: true, empty: false })
    expect(checkParamOverride('{a')).toEqual({ ok: false, reason: 'syntax' })
    expect(checkParamOverride('[1]')).toEqual({
      ok: false,
      reason: 'not-object',
    })
    expect(checkParamOverride('null')).toEqual({
      ok: false,
      reason: 'not-object',
    })
  })

  it('headers need valid names and string values', () => {
    expect(checkHeaderOverride('{"X-A":"b"}').ok).toBe(true)
    expect(checkHeaderOverride('{"bad name":"b"}')).toEqual({
      ok: false,
      reason: 'header-key',
    })
    expect(checkHeaderOverride('{"X-A":2}')).toEqual({
      ok: false,
      reason: 'header-value',
    })
  })

  it('prettyJson leaves invalid text alone', () => {
    expect(prettyJson('{"a":1}')).toBe('{\n  "a": 1\n}')
    expect(prettyJson('{oops')).toBe('{oops')
  })

  it('parseChannelIds dedupes and rejects junk', () => {
    expect(parseChannelIds('1, 2 2,3')).toEqual([1, 2, 3])
    expect(parseChannelIds('')).toBeNull()
    expect(parseChannelIds('1,x')).toBeNull()
    expect(parseChannelIds('0')).toBeNull()
  })
})

describe('mapping', () => {
  it('treats missing arrays as empty and defaults enums', () => {
    expect(mapRuleList({})).toMatchObject({ rules: [], builtins: [] })
    expect(mapTemplateList(null)).toEqual([])
    expect(mapRuleList({ rules: [{ id: 1 }] }).rules[0]).toMatchObject({
      role_scope: 'any',
      kind: 'mask',
      mode: 'observe',
      enabled: true,
    })
    expect(mapApplyOutcome({ results: [{ channel_id: 4 }] }).results).toEqual([
      { channel_id: 4, applied: false, error: '' },
    ])
  })
})

describe('rule form', () => {
  it('builtin rule drops the pattern, regex rule drops the builtin', () => {
    const b = buildRuleBody({
      ...EMPTY_RULE_FORM,
      name: 'n',
      builtin: 'email',
      pattern: 'stale',
    })
    expect(b.ok && b.body).toMatchObject({ builtin: 'email', pattern: '' })
    const r = buildRuleBody({
      ...EMPTY_RULE_FORM,
      name: 'n',
      pattern_type: 'regex',
      builtin: 'stale',
      pattern: 'x+',
    })
    expect(r.ok && r.body).toMatchObject({ builtin: '', pattern: 'x+' })
  })

  it('reports the first missing field', () => {
    expect(buildRuleBody(EMPTY_RULE_FORM)).toEqual({
      ok: false,
      error: 'name',
    })
    expect(buildRuleBody({ ...EMPTY_RULE_FORM, name: 'n' })).toEqual({
      ok: false,
      error: 'builtin',
    })
    expect(
      buildRuleBody({ ...EMPTY_RULE_FORM, name: 'n', ordinal: '-1' })
    ).toEqual({ ok: false, error: 'ordinal' })
  })
})
