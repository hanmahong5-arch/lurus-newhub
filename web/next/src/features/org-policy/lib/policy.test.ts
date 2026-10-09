import { describe, expect, it } from 'vitest'

import {
  auditFilterError,
  auditParams,
  buildRuleBody,
  checkEntry,
  covered,
  emptyRuleForm,
  initialSelection,
  joinCsvPages,
  looser,
  mapAllowlist,
  mapAuditPage,
  mapRetention,
  mapRules,
  pageCount,
} from './policy'

describe('mapAllowlist', () => {
  it('maps the wire shape and tolerates nulls', () => {
    const a = mapAllowlist({
      platform_allowed: ['model-a*', 'model-b'],
      platform_unrestricted: false,
      tenant_selected: null,
      tenant_configured: false,
      effective: ['model-a*', 'model-b'],
    })
    expect(a.platformAllowed).toEqual(['model-a*', 'model-b'])
    expect(a.tenantSelected).toEqual([])
    expect(a.tenantConfigured).toBe(false)
    expect(mapAllowlist(undefined).effective).toEqual([])
  })

  it('starts from the effective list until the tenant has narrowed', () => {
    const open = mapAllowlist({
      platform_unrestricted: true,
      tenant_configured: false,
      effective: ['*'],
    })
    expect(initialSelection(open)).toEqual([])
    const narrowed = mapAllowlist({
      platform_allowed: ['model-a', 'model-b'],
      tenant_selected: ['model-a'],
      tenant_configured: true,
      effective: ['model-a'],
    })
    expect(initialSelection(narrowed)).toEqual(['model-a'])
  })
})

describe('covered / checkEntry (mirror of tenantpolicy.Covered)', () => {
  it('exact entries follow the ceiling, wildcards need a wider wildcard', () => {
    expect(covered(['model-a*'], 'model-a1')).toBe(true)
    expect(covered(['model-a1'], 'model-a*')).toBe(false)
    expect(covered(['model-*'], 'model-a*')).toBe(true)
    expect(covered(['model-a'], 'model-b')).toBe(false)
  })

  it('rejects blank, duplicate, inner wildcard, oversize and ungranted entries', () => {
    expect(checkEntry('  ', [], null)).toBe('blank')
    expect(checkEntry('model-a', ['model-a'], null)).toBe('duplicate')
    expect(checkEntry('mo*del', [], null)).toBe('wildcard')
    expect(checkEntry('x'.repeat(129), [], null)).toBe('too-long')
    expect(checkEntry('model-z', [], ['model-a'])).toBe('not-granted')
    expect(checkEntry('model-a', [], ['model-a'])).toBe('ok')
    expect(checkEntry('anything', [], null)).toBe('ok')
  })
})

describe('retention', () => {
  it('maps and orders strictness', () => {
    expect(
      mapRetention({
        platform_default: 'metadata_only',
        tenant: '',
        effective: 'metadata_only',
      })
    ).toEqual({
      platformDefault: 'metadata_only',
      tenant: '',
      effective: 'metadata_only',
    })
    expect(looser('full', 'metadata_only')).toBe(true)
    expect(looser('none', 'metadata_only')).toBe(false)
    expect(looser('metadata_only', 'metadata_only')).toBe(false)
    // inherit never loosens
    expect(looser('', 'none')).toBe(false)
  })

  it('an unknown server value never reads as a real mode', () => {
    expect(mapRetention({ tenant: 'weird' }).tenant).toBe('')
    expect(mapRetention({}).effective).toBe('full')
  })
})

describe('content rules', () => {
  const builtins = ['phone_cn', 'email']

  it('maps rules and the builtin catalogue from the server', () => {
    const v = mapRules({
      rules: [
        {
          id: 3,
          name: 'phones',
          kind: 'mask',
          pattern_type: 'builtin',
          builtin: 'phone_cn',
          mode: 'observe',
          enabled: false,
          role_scope: 'user',
        },
      ],
      builtins,
      max_rules: 20,
      max_pattern_len: 512,
    })
    expect(v.rules[0]).toMatchObject({
      id: 3,
      roleScope: 'user',
      enabled: false,
      builtin: 'phone_cn',
    })
    expect(v.builtins).toEqual(builtins)
    expect(v.maxRules).toBe(20)
  })

  it('new rules start in observe', () => {
    expect(emptyRuleForm(builtins).mode).toBe('observe')
  })

  it('builtin rule carries no pattern; regex rule carries no builtin', () => {
    const b = buildRuleBody(
      { ...emptyRuleForm(builtins), name: ' phones ', pattern: 'ignored' },
      512
    )
    expect(b).toMatchObject({
      ok: true,
      body: { name: 'phones', builtin: 'phone_cn', pattern: '', mode: 'observe' },
    })
    const r = buildRuleBody(
      {
        ...emptyRuleForm(builtins),
        name: 'ticket',
        patternType: 'regex',
        pattern: 'TK-\\d{6}',
        builtin: 'phone_cn',
        kind: 'reject',
        replacement: 'x',
      },
      512
    )
    expect(r).toMatchObject({
      ok: true,
      body: { builtin: '', pattern: 'TK-\\d{6}', kind: 'reject', replacement: '' },
    })
  })

  it('validates name, pattern, length, syntax and order', () => {
    const base = emptyRuleForm(builtins)
    expect(buildRuleBody({ ...base, name: ' ' }, 512)).toEqual({
      ok: false,
      error: 'name',
    })
    expect(
      buildRuleBody({ ...base, name: 'a', patternType: 'regex' }, 512)
    ).toEqual({ ok: false, error: 'pattern' })
    expect(
      buildRuleBody(
        { ...base, name: 'a', patternType: 'regex', pattern: 'a'.repeat(9) },
        8
      )
    ).toEqual({ ok: false, error: 'pattern-long' })
    expect(
      buildRuleBody(
        { ...base, name: 'a', patternType: 'regex', pattern: '(?i)secret' },
        512
      )
    ).toMatchObject({ ok: true, body: { pattern: "(?i)secret", builtin: "" } })
    expect(buildRuleBody({ ...base, name: 'a', ordinal: '-1' }, 512)).toEqual({
      ok: false,
      error: 'ordinal',
    })
    expect(buildRuleBody({ ...base, name: 'a', builtin: '' }, 512)).toEqual({
      ok: false,
      error: 'builtin',
    })
  })
})

describe('audit', () => {
  it('maps a page and keeps total', () => {
    const p = mapAuditPage({
      items: [{ id: 1, timestamp: 10, action: 'a.b', actor_id: 7 }],
      total: 41,
      page: 2,
      page_size: 20,
    })
    expect(p.items[0]).toMatchObject({ id: 1, action: 'a.b', actorId: 7 })
    expect(p.total).toBe(41)
    expect(pageCount(41)).toBe(3)
    expect(pageCount(0)).toBe(1)
  })

  it('turns filters into the query the handler reads', () => {
    expect(
      auditParams({
        action: ' data_policy.rule_hit ',
        actorId: '5',
        from: '2026-10-01',
        to: '2026-10-02',
      })
    ).toEqual({
      action: 'data_policy.rule_hit',
      actor_id: 5,
      start_time: Math.floor(new Date(2026, 9, 1).getTime() / 1000),
      end_time: Math.floor(new Date(2026, 9, 2).getTime() / 1000) + 86399,
    })
    expect(auditParams({ action: '', actorId: '', from: '', to: '' })).toEqual(
      {}
    )
    expect(
      auditParams({ action: '', actorId: 'abc', from: '', to: '' })
    ).toEqual({})
  })

  it('flags bad operator ids and inverted ranges', () => {
    const f = { action: '', actorId: '', from: '', to: '' }
    expect(auditFilterError({ ...f, actorId: 'x' })).toBe('actor')
    expect(auditFilterError({ ...f, actorId: '0' })).toBe('actor')
    expect(
      auditFilterError({ ...f, from: '2026-10-02', to: '2026-10-01' })
    ).toBe('range')
    expect(auditFilterError({ ...f, from: '2026-10-01' })).toBeNull()
  })

  it('joins CSV pages keeping a single header', () => {
    expect(joinCsvPages(['h\nr1\n', 'h\nr2\n', 'h\n'])).toBe('h\nr1\nr2\n')
    expect(joinCsvPages([])).toBe('')
  })
})
