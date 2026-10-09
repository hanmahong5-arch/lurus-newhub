/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.

For commercial licensing, please contact support@quantumnous.com
*/
import { describe, expect, it } from 'vitest'

import { parseMoneyConfig } from '@/lib/money'

import type { ApiToken } from '../types'
import {
  EMPTY_FORM,
  buildWriteBody,
  dateInputToUnix,
  formFromToken,
} from './form'
import {
  mapProjects,
  mapToken,
  mapTokenList,
  tokenState,
} from './map'
import { parseCapInput, quotaToDisplayInput, quotaView } from './quota'
import {
  buildSnippets,
  clientEndpoints,
  limitToToken,
  relayHost,
} from './snippets'

function must<T>(v: T | null): T {
  if (v === null) throw new Error('config did not parse')
  return v
}

const usd = must(parseMoneyConfig({ quota_per_unit: 500000 }))
const cny = must(
  parseMoneyConfig({
    quota_per_unit: 500000,
    quota_display_type: 'CNY',
    usd_exchange_rate: 7.2,
  })
)

function token(over: Partial<ApiToken> = {}): ApiToken {
  return {
    id: 1,
    name: 'k',
    key: 'sk-a****b',
    status: 1,
    created_time: 0,
    accessed_time: 0,
    expired_time: -1,
    remain_quota: 0,
    used_quota: 0,
    unlimited_quota: false,
    model_limits_enabled: false,
    model_limits: '',
    allow_ips: '',
    project_id: 0,
    ...over,
  }
}

describe('mapToken / mapTokenList', () => {
  it('reads the v2 tokenView fields, tolerating a null allow_ips', () => {
    const t = mapToken({
      id: 7,
      name: 'prod',
      key: 'sk-ab****yz',
      status: 1,
      expired_time: -1,
      remain_quota: 100,
      used_quota: 5,
      unlimited_quota: false,
      allow_ips: null,
      project_id: 3,
    })
    expect(t).toMatchObject({
      id: 7,
      name: 'prod',
      allow_ips: '',
      project_id: 3,
    })
  })

  it('maps the page envelope and treats missing items as an empty page', () => {
    const page = mapTokenList({ items: [{ id: 1 }], total: 41, page: 2, page_size: 20 })
    expect(page).toMatchObject({ total: 41, page: 2, pageSize: 20 })
    expect(page.items).toHaveLength(1)
    expect(mapTokenList({}).items).toEqual([])
    expect(mapTokenList(null).total).toBe(0)
  })

  it('drops deleted and id-less projects', () => {
    expect(
      mapProjects({
        items: [
          { id: 1, name: 'a' },
          { id: 2, name: 'b', deleted: true },
          { name: 'c' },
        ],
      })
    ).toEqual([{ id: 1, name: 'a' }])
  })
})

describe('tokenState', () => {
  const now = 1_700_000_000
  it('derives expiry and exhaustion that the server flips lazily', () => {
    expect(tokenState(token({ unlimited_quota: true }), now)).toBe('enabled')
    expect(tokenState(token({ status: 2 }), now)).toBe('disabled')
    expect(tokenState(token({ status: 3 }), now)).toBe('expired')
    expect(
      tokenState(token({ expired_time: now - 1, unlimited_quota: true }), now)
    ).toBe('expired')
    expect(tokenState(token({ remain_quota: 0 }), now)).toBe('exhausted')
    expect(
      tokenState(token({ used_quota: 95, remain_quota: 5 }), now)
    ).toBe('near-cap')
    expect(tokenState(token({ used_quota: 10, remain_quota: 90 }), now)).toBe(
      'enabled'
    )
  })

  it('an unlimited key with remain_quota 0 is not exhausted', () => {
    expect(
      tokenState(token({ unlimited_quota: true, remain_quota: 0 }), now)
    ).toBe('enabled')
  })
})

describe('quota', () => {
  it('shows an unlimited key as unlimited, never as a zero balance', () => {
    const q = quotaView(token({ unlimited_quota: true, used_quota: 500000 }), usd)
    expect(q.unlimited).toBe(true)
    expect(q.used).toBe('$1.00')
    expect(q.remaining).toBe('--')
    expect(q.total).toBe('--')
  })

  it('limited key: total = used + remaining, using quota_per_unit', () => {
    const q = quotaView(token({ used_quota: 250000, remain_quota: 750000 }), usd)
    expect(q.total).toBe('$2.00')
    expect(q.remaining).toBe('$1.50')
    expect(q.usedRatio).toBeCloseTo(0.25)
  })

  it('does not invent amounts before the unit price is known', () => {
    expect(quotaView(token({ remain_quota: 5 }), null).remaining).toBe('--')
  })

  it('round-trips a typed cap through the unit price and display rate', () => {
    expect(parseCapInput('2', usd)).toEqual({ ok: true, quota: 1_000_000 })
    expect(parseCapInput('7.2', cny)).toEqual({ ok: true, quota: 500_000 })
    expect(quotaToDisplayInput(500_000, cny)).toBe('7.2')
    expect(quotaToDisplayInput(1_250_000, usd)).toBe('2.5')
  })

  it('rejects empty, zero and malformed caps instead of saving unlimited', () => {
    for (const bad of ['', '0', '-1', 'abc', '1e3', '$5']) {
      expect(parseCapInput(bad, usd)).toEqual({ ok: false, reason: 'invalid' })
    }
    expect(parseCapInput('5', null)).toEqual({
      ok: false,
      reason: 'unavailable',
    })
  })
})

describe('buildWriteBody', () => {
  it('create: unlimited key sends unlimited and zero remain', () => {
    const r = buildWriteBody({ ...EMPTY_FORM, name: ' prod ' }, usd, 0)
    expect(r).toMatchObject({
      ok: true,
      body: {
        name: 'prod',
        unlimited_quota: true,
        remain_quota: 0,
        expired_time: -1,
        project_id: 0,
      },
    })
    // Rate limits are not exposed by the v2 token view: never sent, so an
    // edit cannot reset a limit that was set elsewhere.
    expect(r.ok && 'rate_limit_rpm' in r.body).toBe(false)
    expect(r.ok && 'rate_limit_tpm' in r.body).toBe(false)
  })

  it('edit: the cap is a total, the wire carries the remainder', () => {
    const t = token({ used_quota: 250000, remain_quota: 750000 })
    const form = { ...formFromToken(t, usd), cap: '3' }
    const r = buildWriteBody(form, usd, t.used_quota)
    expect(r.ok && r.body.remain_quota).toBe(1_250_000)
    expect(r.ok && r.body.unlimited_quota).toBe(false)
  })

  it('edit: a cap below current spend leaves zero remaining, not negative', () => {
    const r = buildWriteBody(
      { ...EMPTY_FORM, name: 'k', unlimited: false, cap: '0.1' },
      usd,
      500000
    )
    expect(r.ok && r.body.remain_quota).toBe(0)
  })

  it('reports field errors', () => {
    expect(buildWriteBody(EMPTY_FORM, usd, 0)).toEqual({ ok: false, error: 'name' })
    const base = { ...EMPTY_FORM, name: 'k' }
    expect(buildWriteBody({ ...base, unlimited: false }, usd, 0)).toEqual({
      ok: false,
      error: 'cap-invalid',
    })
    expect(buildWriteBody({ ...base, unlimited: false, cap: '5' }, null, 0)).toEqual({
      ok: false,
      error: 'cap-unavailable',
    })
    expect(buildWriteBody({ ...base, limitModels: true }, usd, 0)).toEqual({
      ok: false,
      error: 'models',
    })
  })

  it('maps a date to the end of that local day, empty to never', () => {
    expect(dateInputToUnix('')).toBe(-1)
    expect(dateInputToUnix('2030-01-02')).toBe(
      Math.floor(new Date('2030-01-02T23:59:59').getTime() / 1000)
    )
    expect(Number.isNaN(dateInputToUnix('nope'))).toBe(true)
  })
})

describe('snippets', () => {
  it('uses the configured host, else the origin, without a trailing slash', () => {
    expect(relayHost('', 'https://hub.example/')).toBe('https://hub.example')
    expect(relayHost('https://api.example//', 'https://x')).toBe(
      'https://api.example'
    )
    expect(relayHost(undefined, 'http://localhost:5173')).toBe(
      'http://localhost:5173'
    )
  })

  it('builds examples from the host and omits the messages example without a model', () => {
    const s = buildSnippets('sk-1', 'https://h', 'm1', null)
    expect(s.curl).toContain('https://h/v1/chat/completions')
    expect(s.curl).toContain('Bearer sk-1')
    expect(s.python).toContain('https://h/v1/chat/completions')
    expect(s.anthropic).toBe('')
    expect(buildSnippets('k', 'https://h', 'm1', 'c1').anthropic).toContain(
      '"model":"c1"'
    )
    expect(clientEndpoints('https://h').map((e) => e[1])).toEqual([
      'https://h/v1',
      'https://h/v1/messages',
      'https://h/v1beta',
    ])
  })

  it('narrows models to a key allowlist', () => {
    const models = [
      { id: 'a', supported_endpoint_types: ['openai'] },
      { id: 'b', supported_endpoint_types: ['openai'] },
    ]
    expect(
      limitToToken(models, { model_limits_enabled: true, model_limits: 'b, c' })
    ).toEqual([models[1]])
    expect(
      limitToToken(models, { model_limits_enabled: false, model_limits: 'b' })
    ).toEqual(models)
  })
})
