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

import { tenantFixture } from '../test-helpers'
import {
  EMPTY_CREATE_FORM,
  editFormFromTenant,
  limitFieldValue,
  parseCreateForm,
  parseEditForm,
  parseLimit,
  planChoices,
  slugify,
  type CreateFormValues,
  type EditFormValues,
} from './tenant-form'
import { tenantMatchesSearch } from './search'

const fromDisplay = (amount: number) => Math.round(amount * 500_000)
const toDisplay = (quota: number) => quota / 500_000

const create = (over: Partial<CreateFormValues> = {}): CreateFormValues => ({
  ...EMPTY_CREATE_FORM,
  name: 'NewCo',
  slug: 'newco',
  orgId: 'org-1',
  ...over,
})

describe('slugify', () => {
  it('lower-cases and turns anything outside a-z 0-9 - into a dash', () => {
    expect(slugify('Acme Corp')).toBe('acme-corp')
    expect(slugify('a_b.c')).toBe('a-b-c')
    expect(slugify('already-fine-9')).toBe('already-fine-9')
  })
})

describe('parseCreateForm', () => {
  it('sends the server wire names, never camelCase or the old legacy keys', () => {
    const parsed = parseCreateForm(create({ plan: 'pro' }), fromDisplay)
    expect(parsed).toEqual({
      ok: true,
      body: {
        zitadel_org_id: 'org-1',
        slug: 'newco',
        name: 'NewCo',
        plan_type: 'pro',
      },
    })
    if (!parsed.ok) return
    for (const stale of ['quotaLimit', 'planType', 'plan', 'quota_limit']) {
      expect(parsed.body).not.toHaveProperty(stale)
    }
  })

  it('converts the money cap to quota units and carries seats and limits', () => {
    const parsed = parseCreateForm(
      create({ quotaCap: '5', maxUsers: '25', rpm: '60', tpm: '9000' }),
      fromDisplay
    )
    expect(parsed).toMatchObject({
      ok: true,
      body: {
        max_quota: 2_500_000,
        max_users: 25,
        rate_limit_rpm: 60,
        rate_limit_tpm: 9000,
      },
    })
  })

  it('leaves blank optional fields out so the server applies its defaults', () => {
    const parsed = parseCreateForm(create({ rpm: '0' }), fromDisplay)
    expect(parsed.ok).toBe(true)
    if (!parsed.ok) return
    expect(parsed.body).not.toHaveProperty('max_quota')
    expect(parsed.body).not.toHaveProperty('max_users')
    expect(parsed.body).not.toHaveProperty('rate_limit_rpm')
  })

  it('names and slug and org id are required', () => {
    const parsed = parseCreateForm(EMPTY_CREATE_FORM, fromDisplay)
    expect(parsed).toEqual({
      ok: false,
      errors: { name: 'required', slug: 'required', orgId: 'required' },
    })
  })

  it('refuses over-long text, bad numbers and an unavailable currency config', () => {
    expect(
      parseCreateForm(create({ name: 'x'.repeat(129) }), fromDisplay)
    ).toMatchObject({ ok: false, errors: { name: 'too-long' } })
    expect(
      parseCreateForm(create({ slug: 'x'.repeat(65) }), fromDisplay)
    ).toMatchObject({ ok: false, errors: { slug: 'too-long' } })
    expect(
      parseCreateForm(create({ maxUsers: '0', rpm: '-3', tpm: 'x' }), fromDisplay)
    ).toMatchObject({
      ok: false,
      errors: {
        maxUsers: 'invalid-number',
        rpm: 'invalid-number',
        tpm: 'invalid-number',
      },
    })
    expect(
      parseCreateForm(create({ quotaCap: 'abc' }), fromDisplay)
    ).toMatchObject({ ok: false, errors: { quotaCap: 'invalid-amount' } })
    expect(parseCreateForm(create({ quotaCap: '5' }), () => null)).toMatchObject(
      { ok: false, errors: { quotaCap: 'currency' } }
    )
  })
})

describe('parseEditForm', () => {
  const tenant = tenantFixture({ max_quota: 1_000_000, max_users: 100 })
  const initial = editFormFromTenant(tenant, toDisplay)

  it('prefills from the tenant, with the cap in display money', () => {
    expect(initial).toEqual({
      name: 'Acme Corp',
      plan: 'free',
      maxUsers: '100',
      quotaCap: '2',
      walletAuthoritative: false,
    })
  })

  it('an untouched form changes nothing', () => {
    expect(parseEditForm(initial, initial, fromDisplay)).toEqual({
      ok: true,
      patch: {},
    })
  })

  it('sends only what changed, in wire names and quota units', () => {
    const values: EditFormValues = {
      ...initial,
      name: '  Acme Inc  ',
      plan: 'enterprise',
      quotaCap: '10',
      walletAuthoritative: true,
    }
    expect(parseEditForm(values, initial, fromDisplay)).toEqual({
      ok: true,
      patch: {
        name: 'Acme Inc',
        plan_type: 'enterprise',
        max_quota: 5_000_000,
        wallet_authoritative: true,
      },
    })
  })

  it('refuses to clear what the server cannot clear', () => {
    expect(
      parseEditForm({ ...initial, name: ' ' }, initial, fromDisplay)
    ).toMatchObject({ ok: false, errors: { name: 'required' } })
    expect(
      parseEditForm({ ...initial, quotaCap: '' }, initial, fromDisplay)
    ).toMatchObject({ ok: false, errors: { quotaCap: 'cannot-clear' } })
    expect(
      parseEditForm({ ...initial, maxUsers: '' }, initial, fromDisplay)
    ).toMatchObject({ ok: false, errors: { maxUsers: 'cannot-clear' } })
  })

  it('a tenant with no cap yet can be given one', () => {
    const open = editFormFromTenant(tenantFixture({ max_quota: 0 }), toDisplay)
    expect(open.quotaCap).toBe('')
    expect(
      parseEditForm({ ...open, quotaCap: '1' }, open, fromDisplay)
    ).toEqual({ ok: true, patch: { max_quota: 500_000 } })
  })
})

describe('rate limit fields', () => {
  it('a blank or junk field is 0 (unlimited); fractions truncate; negatives floor at 0', () => {
    expect(parseLimit('')).toBe(0)
    expect(parseLimit('abc')).toBe(0)
    expect(parseLimit('5000')).toBe(5000)
    expect(parseLimit('12.9')).toBe(12)
    expect(parseLimit('-4')).toBe(0)
  })

  it('0 is shown as an empty field, not a literal 0', () => {
    expect(limitFieldValue(0)).toBe('')
    expect(limitFieldValue(undefined)).toBe('')
    expect(limitFieldValue(60)).toBe('60')
  })
})

describe('planChoices', () => {
  it('offers the platform plans, plus the tenant’s own when outside them', () => {
    expect(planChoices()).toEqual(['free', 'pro', 'enterprise'])
    expect(planChoices('pro')).toEqual(['free', 'pro', 'enterprise'])
    expect(planChoices('legacy-team')).toEqual([
      'free',
      'pro',
      'enterprise',
      'legacy-team',
    ])
  })
})

describe('tenantMatchesSearch', () => {
  const tenant = tenantFixture({
    name: 'Global Inc',
    slug: 'global',
    id: 'uuid-9',
    zitadel_org_id: 'org-77',
  })

  it('matches name, slug, id and org id, ignoring case', () => {
    for (const q of ['global inc', 'GLOBAL', 'uuid-9', 'org-77']) {
      expect(tenantMatchesSearch(tenant, q)).toBe(true)
    }
    expect(tenantMatchesSearch(tenant, 'nope')).toBe(false)
  })

  it('a blank query matches everything', () => {
    expect(tenantMatchesSearch(tenant, '  ')).toBe(true)
  })
})
