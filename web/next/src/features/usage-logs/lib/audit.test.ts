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

import {
  EMPTY_AUDIT_FILTERS,
  auditFilterError,
  auditParams,
  chainIntact,
  joinCsvPages,
  mapChainReport,
  mapPlatformAuditPage,
  mapTenantAuditPage,
  parseDetails,
} from './audit'

describe('feeds', () => {
  it('maps the tenant shape (items) and the platform shape (events)', () => {
    expect(
      mapTenantAuditPage({ items: [{ id: 1, action: 'a' }], total: 7 })
    ).toMatchObject({ total: 7, items: [{ id: 1, action: 'a' }] })
    expect(
      mapPlatformAuditPage({ events: [{ id: 2, tenant_id: 't', row_hash: 'h' }], total: 3 })
    ).toMatchObject({ total: 3, items: [{ tenantId: 't', rowHash: 'h' }] })
    expect(mapPlatformAuditPage({ events: null }).items).toEqual([])
  })
})

describe('filters', () => {
  it('maps to the real query names; resource is platform-only', () => {
    const f = {
      ...EMPTY_AUDIT_FILTERS,
      action: ' a.b ',
      actorId: '4',
      resource: 'token',
      start: '2026-01-02T00:00',
    }
    const tenant = auditParams(f, 'tenant')
    expect(tenant.action).toBe('a.b')
    expect(tenant.actor_id).toBe(4)
    expect('resource' in tenant).toBe(false)
    expect(auditParams(f, 'platform').resource).toBe('token')
  })

  it('flags a bad operator and an inverted window', () => {
    expect(auditFilterError({ ...EMPTY_AUDIT_FILTERS, actorId: 'x' })).toBe('actor')
    expect(auditFilterError({ ...EMPTY_AUDIT_FILTERS, actorId: '-2' })).toBe('actor')
    expect(
      auditFilterError({
        ...EMPTY_AUDIT_FILTERS,
        start: '2026-02-01T00:00',
        end: '2026-01-01T00:00',
      })
    ).toBe('range')
    expect(auditFilterError(EMPTY_AUDIT_FILTERS)).toBeNull()
  })
})

describe('details and chain', () => {
  it('reads JSON objects as rows and anything else as text', () => {
    const o = parseDetails('{"a":1,"b":{"c":2}}')
    expect(o.kind).toBe('object')
    if (o.kind === 'object') {
      expect(o.entries.map((e) => [e.key, e.nested])).toEqual([
        ['a', false],
        ['b', true],
      ])
    }
    expect(parseDetails('plain words')).toEqual({ kind: 'text', text: 'plain words' })
    expect(parseDetails('  ')).toEqual({ kind: 'empty' })
  })

  it('a window with any break is not intact', () => {
    const base = mapChainReport({ checked: 5 })
    expect(chainIntact(base)).toBe(true)
    expect(chainIntact(mapChainReport({ hash_breaks: 1 }))).toBe(false)
    expect(chainIntact(mapChainReport({ link_breaks: 2 }))).toBe(false)
  })

  it('drops repeated CSV headers when stitching pages', () => {
    expect(joinCsvPages(['h\n1\n', 'h\n2\n'])).toBe('h\n1\n2\n')
  })
})
