import { describe, expect, it } from 'vitest'

import { visibleClassicGroup } from './classic-nav'

const ids = (user: Parameters<typeof visibleClassicGroup>[0]) =>
  visibleClassicGroup(user)?.items.map((i) => i.id) ?? null

describe('visibleClassicGroup', () => {
  it('hides the whole group from a plain member', () => {
    expect(ids({ role: 1, tenant_role: '' })).toBeNull()
    expect(ids(undefined)).toBeNull()
  })

  it('gives a tenant admin only the tenant-level legacy pages', () => {
    expect(ids({ role: 1, tenant_role: 'admin' })).toEqual([
      'classic-flows',
      'classic-pricing',
    ])
  })

  it('does not show root-only pages to platform staff below root', () => {
    expect(ids({ role: 10, tenant_role: '' })).toEqual([
      'classic-flows',
      'classic-pricing',
    ])
  })

  it('shows every legacy page to root, all as plain links', () => {
    const group = visibleClassicGroup({ role: 100, tenant_role: '' })
    expect(group?.items.map((i) => i.id)).toEqual([
      'classic-flows',
      'classic-pricing',
      'classic-tenants',
      'classic-model-limits',
      'classic-authz',
      'classic-model-pools',
      'classic-cost-intelligence',
      'classic-diagnostics',
      'classic-system-tasks',
      'classic-openrouter-sync',
    ])
    for (const item of group?.items ?? []) {
      expect(item.href).toMatch(/^\/console\//)
    }
  })
})
