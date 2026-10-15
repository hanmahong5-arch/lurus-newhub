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
import fs from 'node:fs'
import path from 'node:path'
import { fileURLToPath } from 'node:url'

import type { TFunction } from 'i18next'
import { beforeEach, describe, expect, it, vi } from 'vitest'

import { ApiError } from '@/lib/api'
import type { MoneyConfig } from '@/lib/money'

import {
  CHANNEL_FORM_INIT,
  baseUrlAfterVendorPick,
  buildChannelBody,
  canCreateChannel,
  describeChannelError,
  mergeModels,
  needsBaseUrl,
  otherKind,
  splitModels,
  validateChannelForm,
} from './lib/channel'
import {
  TOKEN_DRAFT_INIT,
  TOKEN_DRAFT_KEY,
  buildTokenBody,
  describeTokenError,
  expiryToUnix,
  loadTokenDraft,
  saveTokenDraft,
  validateLimits,
  validateScope,
} from './lib/token'
import {
  NO_DEFAULT_BASE_URL_TYPES,
  VENDOR_CHOICES,
  presetBaseUrl,
} from './lib/vendors'
import { MISSING_KEY } from './types'

const t = ((key: string) => key) as unknown as TFunction

const USD: MoneyConfig = {
  quotaPerUnit: 500000,
  displayType: 'USD',
  symbol: '$',
  rate: 1,
}
const CNY: MoneyConfig = {
  quotaPerUnit: 500000,
  displayType: 'CNY',
  symbol: '¥',
  rate: 7.3,
}
const TOKENS: MoneyConfig = {
  quotaPerUnit: 500000,
  displayType: 'TOKENS',
  symbol: '',
  rate: 1,
}

describe('vendors', () => {
  it('every vendor card has a unique key and type', () => {
    expect(new Set(VENDOR_CHOICES.map((v) => v.key)).size).toBe(
      VENDOR_CHOICES.length
    )
    expect(new Set(VENDOR_CHOICES.map((v) => v.type)).size).toBe(
      VENDOR_CHOICES.length
    )
  })

  it('the four hostless vendors have no preset and are flagged as required', () => {
    for (const type of [3, 8, 33, 41]) {
      expect(NO_DEFAULT_BASE_URL_TYPES.has(type)).toBe(true)
      expect(presetBaseUrl(type)).toBe('')
      expect(needsBaseUrl(type)).toBe(true)
    }
    expect(needsBaseUrl(14)).toBe(false)
  })

  it('presets match the vendors the wizard offers', () => {
    expect(presetBaseUrl(14)).toBe('https://api.anthropic.com')
    expect(presetBaseUrl(26)).toBe('https://open.bigmodel.cn')
    expect(presetBaseUrl(40)).toBe('https://api.siliconflow.cn')
    expect(presetBaseUrl(1)).toBe('')
  })
})

describe('channel form', () => {
  it('splitModels trims, drops blanks and de-duplicates in order', () => {
    expect(splitModels(' a, b ,,a ,c')).toEqual(['a', 'b', 'c'])
    expect(splitModels('')).toEqual([])
  })

  it('mergeModels adds the selection to the current list, never overwrites', () => {
    expect(mergeModels('a,b', ['c', 'a'])).toBe('a,b,c')
    expect(mergeModels('', ['x'])).toBe('x')
    expect(mergeModels(' a ', new Set(['b']))).toBe('a,b')
  })

  it('baseUrlAfterVendorPick keeps typed input and replaces a preset', () => {
    expect(baseUrlAfterVendorPick(26, 'https://api.anthropic.com', false)).toBe(
      'https://open.bigmodel.cn'
    )
    expect(baseUrlAfterVendorPick(26, 'https://mine', true)).toBe(
      'https://mine'
    )
    expect(baseUrlAfterVendorPick(8, 'https://api.anthropic.com', false)).toBe(
      ''
    )
  })

  const ok = {
    ...CHANNEL_FORM_INIT,
    name: 'n',
    key: 'sk-1',
    models: 'a',
  }

  it('validateChannelForm: a pasted multi-key blob is refused first', () => {
    expect(validateChannelForm({ ...ok, key: 'k1\nk2' })).toBe('multi_key')
    expect(validateChannelForm({ ...ok, key: 'k1\r\nk2' })).toBe('multi_key')
    // Surrounding whitespace alone is fine (trim handles it).
    expect(validateChannelForm({ ...ok, key: '  k1\n' })).toBeNull()
    expect(
      validateChannelForm({ ...ok, type: 8, key: 'k1\nk2', baseURL: '' })
    ).toBe('multi_key')
  })

  it('validateChannelForm: hostless vendors need a base url', () => {
    expect(validateChannelForm({ ...ok, type: 8 })).toBe('base_url_required')
    expect(validateChannelForm({ ...ok, type: 8, baseURL: '  ' })).toBe(
      'base_url_required'
    )
    expect(
      validateChannelForm({ ...ok, type: 8, baseURL: 'https://x' })
    ).toBeNull()
    expect(validateChannelForm({ ...ok, type: 14 })).toBeNull()
  })

  it('validateChannelForm: Vertex needs a JSON region with a default entry', () => {
    const v = { ...ok, type: 41, baseURL: 'https://v' }
    expect(validateChannelForm(v)).toBe('other_required')
    expect(validateChannelForm({ ...v, other: 'us-central1' })).toBe(
      'other_invalid'
    )
    expect(validateChannelForm({ ...v, other: '[]' })).toBe('other_invalid')
    expect(validateChannelForm({ ...v, other: '{"region2":"x"}' })).toBe(
      'other_invalid'
    )
    expect(validateChannelForm({ ...v, other: '{"default":"us"}' })).toBeNull()
  })

  it('canCreateChannel needs name, key and models', () => {
    expect(canCreateChannel(ok)).toBe(true)
    expect(canCreateChannel({ ...ok, name: ' ' })).toBe(false)
    expect(canCreateChannel({ ...ok, key: '' })).toBe(false)
    expect(canCreateChannel({ ...ok, models: '' })).toBe(false)
  })

  it('buildChannelBody trims, defaults type and adds optional fields', () => {
    expect(
      buildChannelBody({
        ...ok,
        name: ' n ',
        key: ' k ',
        models: ' a,b ',
        baseURL: ' https://x ',
        orgId: ' org ',
      })
    ).toEqual({
      name: 'n',
      type: 1,
      base_url: 'https://x',
      key: 'k',
      models: 'a,b',
      openai_organization: 'org',
    })
    expect(buildChannelBody(ok)).toEqual({
      name: 'n',
      type: 1,
      base_url: '',
      key: 'sk-1',
      models: 'a',
    })
  })

  it('`other` is only sent for vendors that use it', () => {
    expect(buildChannelBody({ ...ok, type: 1, other: 'x' })).not.toHaveProperty(
      'other'
    )
    expect(buildChannelBody({ ...ok, type: 3, other: ' 2024-02 ' }).other).toBe(
      '2024-02'
    )
    expect(otherKind(41)).toBe('region')
    expect(otherKind(3)).toBe('api-version')
    expect(otherKind(14)).toBeNull()
  })

  it('describeChannelError names the grant for a 403 PERMISSION_DENIED only', () => {
    const denied = new ApiError('insufficient permission', {
      status: 403,
      code: 'PERMISSION_DENIED',
    })
    expect(describeChannelError(denied, t)).toMatch(/channel:sensitive_write/)
    // A 403 with another code keeps the server's own words.
    expect(
      describeChannelError(
        new ApiError('Access denied', { status: 403, code: 'FORBIDDEN' }),
        t
      )
    ).toBe('Access denied')
    expect(describeChannelError(new Error('Network Error'), t)).toBe(
      'Network Error'
    )
    expect(describeChannelError({}, t)).toBe('Failed to reach the channel API')
    expect(describeChannelError(new Error('   '), t)).toBe(
      'Failed to reach the channel API'
    )
  })
})

describe('token form', () => {
  beforeEach(() => window.localStorage.clear())

  it('validateScope: required and at most 50 UTF-8 bytes', () => {
    expect(validateScope({ ...TOKEN_DRAFT_INIT, name: '' })).toBe(
      'name_required'
    )
    expect(validateScope({ ...TOKEN_DRAFT_INIT, name: '  ' })).toBe(
      'name_required'
    )
    expect(
      validateScope({ ...TOKEN_DRAFT_INIT, name: 'a'.repeat(50) })
    ).toBeNull()
    expect(validateScope({ ...TOKEN_DRAFT_INIT, name: 'a'.repeat(51) })).toBe(
      'name_too_long'
    )
    expect(validateScope({ ...TOKEN_DRAFT_INIT, name: '密'.repeat(17) })).toBe(
      'name_too_long'
    )
  })

  it('expiryToUnix: blank = never, a date = end of that local day, junk = NaN', () => {
    expect(expiryToUnix('')).toBe(-1)
    expect(expiryToUnix('2030-06-15')).toBe(
      Math.floor(new Date('2030-06-15T23:59:59').getTime() / 1000)
    )
    expect(expiryToUnix('15/06/2030')).toBeNaN()
    expect(expiryToUnix('2030-13-45')).toBeNaN()
  })

  const now = Math.floor(new Date('2026-01-01T00:00:00').getTime() / 1000)
  const limited = (cap: string, over = {}) => ({
    ...TOKEN_DRAFT_INIT,
    unlimited: false,
    cap,
    ...over,
  })

  it('validateLimits converts the typed amount into internal units', () => {
    expect(validateLimits(limited('0.5'), USD, now)).toMatchObject({
      ok: true,
      quota: 250000,
    })
    expect(validateLimits(limited('12.50'), USD, now)).toMatchObject({
      quota: 6250000,
    })
    // CNY at 7.3: ¥7.3 is $1 is one quota unit.
    expect(validateLimits(limited('7.3'), CNY, now)).toMatchObject({
      quota: 500000,
    })
    // Raw token display: the amount is already in quota units.
    expect(validateLimits(limited('1234'), TOKENS, now)).toMatchObject({
      quota: 1234,
    })
  })

  it('validateLimits refuses a non-positive, malformed or oversized cap', () => {
    for (const bad of ['', ' ', '0', '0.0', '-1', '1e3', '1,5', 'abc', '.5']) {
      expect(validateLimits(limited(bad), USD, now)).toEqual({
        ok: false,
        issue: 'cap_invalid',
      })
    }
    // Rounds to zero units.
    expect(validateLimits(limited('0.0000001'), USD, now)).toEqual({
      ok: false,
      issue: 'cap_invalid',
    })
    expect(validateLimits(limited('9999999999999'), USD, now)).toEqual({
      ok: false,
      issue: 'cap_too_large',
    })
    expect(validateLimits(limited('5'), null, now)).toEqual({
      ok: false,
      issue: 'cap_unavailable',
    })
  })

  it('validateLimits ignores the cap field for an unlimited key', () => {
    expect(
      validateLimits({ ...TOKEN_DRAFT_INIT, cap: 'garbage' }, null, now)
    ).toEqual({ ok: true, quota: 0, expiredTime: -1 })
  })

  it('validateLimits checks the expiry', () => {
    expect(
      validateLimits({ ...TOKEN_DRAFT_INIT, expires: 'nope' }, USD, now)
    ).toEqual({ ok: false, issue: 'expiry_invalid' })
    expect(
      validateLimits({ ...TOKEN_DRAFT_INIT, expires: '2025-12-31' }, USD, now)
    ).toEqual({ ok: false, issue: 'expiry_past' })
    expect(
      validateLimits({ ...TOKEN_DRAFT_INIT, expires: '2026-01-01' }, USD, now)
    ).toMatchObject({ ok: true })
  })

  it('buildTokenBody: default group, zero quota when unlimited', () => {
    expect(
      buildTokenBody(
        { ...TOKEN_DRAFT_INIT, name: ' k ' },
        { quota: 999, expiredTime: -1 }
      )
    ).toEqual({
      name: 'k',
      group: 'default',
      unlimited_quota: true,
      remain_quota: 0,
      expired_time: -1,
    })
    expect(
      buildTokenBody(
        { ...TOKEN_DRAFT_INIT, name: 'k', group: ' g ', unlimited: false },
        { quota: 250000, expiredTime: 1900000000 }
      )
    ).toEqual({
      name: 'k',
      group: 'g',
      unlimited_quota: false,
      remain_quota: 250000,
      expired_time: 1900000000,
    })
  })

  it('draft storage round-trips, tolerates junk and never throws', () => {
    const d = {
      name: 'a',
      group: 'b',
      unlimited: false,
      cap: '3',
      expires: '2030-01-01',
    }
    saveTokenDraft(d)
    expect(loadTokenDraft()).toEqual(d)

    window.localStorage.setItem(TOKEN_DRAFT_KEY, 'null')
    expect(loadTokenDraft()).toEqual(TOKEN_DRAFT_INIT)
    window.localStorage.setItem(TOKEN_DRAFT_KEY, '[1]')
    expect(loadTokenDraft().name).toBe('')
    window.localStorage.setItem(TOKEN_DRAFT_KEY, '{"name":5,"unlimited":"x"}')
    expect(loadTokenDraft()).toEqual(TOKEN_DRAFT_INIT)
    // The old console stored raw units; those fields are ignored, not trusted.
    window.localStorage.setItem(
      TOKEN_DRAFT_KEY,
      JSON.stringify({
        name: 'old',
        remain_quota: 500000,
        expires_at: '2030-01-01',
      })
    )
    expect(loadTokenDraft()).toEqual({ ...TOKEN_DRAFT_INIT, name: 'old' })

    const spy = vi
      .spyOn(Storage.prototype, 'setItem')
      .mockImplementation(() => {
        throw new Error('quota')
      })
    expect(() => saveTokenDraft(d)).not.toThrow()
    spy.mockRestore()
  })

  it('describeTokenError prefers the server message', () => {
    expect(describeTokenError(new ApiError('Token name too long'), t)).toBe(
      'Token name too long'
    )
    expect(
      describeTokenError(new ApiError('x', { code: MISSING_KEY }), t)
    ).toBe('The server did not return the new key')
    expect(describeTokenError({}, t)).toBe('Failed to create key')
  })
})

describe('locales', () => {
  const here = path.dirname(fileURLToPath(import.meta.url))
  const read = (name: string) =>
    JSON.parse(
      fs.readFileSync(path.join(here, 'locales', name), 'utf8')
    ) as Record<string, string>

  /** Every literal `t('...')` first argument in the feature's sources. */
  function usedKeys(): Set<string> {
    const out = new Set<string>()
    const walk = (dir: string) => {
      for (const e of fs.readdirSync(dir, { withFileTypes: true })) {
        const p = path.join(dir, e.name)
        if (e.isDirectory()) walk(p)
        else if (/\.tsx?$/.test(e.name) && !/\.test\./.test(e.name)) {
          const src = fs.readFileSync(p, 'utf8')
          for (const m of src.matchAll(/\bt\(\s*'((?:[^'\\]|\\.)*)'/g)) {
            out.add(m[1].split('\\' + "'").join("'"))
          }
        }
      }
    }
    walk(here)
    return out
  }

  it('zh translates every string the feature shows, and nothing stale', () => {
    const zh = read('zh.json')
    const used = usedKeys()
    expect(used.size).toBeGreaterThan(80)
    const missing = [...used].filter((k) => !(k in zh))
    const stale = Object.keys(zh).filter((k) => !used.has(k))
    expect(missing).toEqual([])
    expect(stale).toEqual([])
  })

  it('keeps interpolation variables intact in every translation', () => {
    const vars = (s: string) => (s.match(/\{\{\w+\}\}/g) ?? []).sort()
    for (const [k, v] of Object.entries(read('zh.json'))) {
      expect(vars(v), k).toEqual(vars(k))
    }
  })

  it('en carries nothing: the keys are the English source', () => {
    expect(read('en.json')).toEqual({})
  })
})
