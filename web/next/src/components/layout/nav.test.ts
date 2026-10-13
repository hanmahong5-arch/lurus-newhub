import { describe, expect, it } from 'vitest'

import { userAccess } from '@/lib/user'

import { visibleNavGroups } from './nav'

const ids = (user: Parameters<typeof userAccess>[0]) =>
  visibleNavGroups(userAccess(user)).map((g) => [
    g.id,
    g.items.map((i) => i.id),
  ])

const WORKBENCH = [
  'dashboard',
  'playground',
  'keys',
  'usage-logs',
  'tasks',
  'models',
]
const WORKBENCH_ADMIN = [...WORKBENCH, 'rankings']
const PERSONAL = ['personal', ['wallet', 'account']]

describe('visibleNavGroups', () => {
  it('plain member sees the workbench and personal pages only', () => {
    expect(ids({ role: 1, tenant_role: '' })).toEqual([
      ['workspace', WORKBENCH],
      PERSONAL,
    ])
  })

  it('department lead sees Departments and Billing only under enterprise', () => {
    expect(ids({ role: 1, tenant_role: 'dept_lead' })).toEqual([
      ['workspace', WORKBENCH],
      PERSONAL,
      ['org', ['org-departments', 'org-billing']],
    ])
  })

  it('tenant admin sees the full enterprise group but no platform ops', () => {
    expect(ids({ role: 1, tenant_role: 'admin' })).toEqual([
      ['workspace', WORKBENCH_ADMIN],
      PERSONAL,
      [
        'org',
        [
          'org-members',
          'org-departments',
          'org-keys-batch',
          'org-billing',
          'org-policy',
          'org-audit',
          'redemption-codes',
        ],
      ],
    ])
  })

  it('platform staff sees every group', () => {
    const groups = ids({ role: 10, tenant_role: '' })
    expect(groups.map((g) => g[0])).toEqual([
      'workspace',
      'personal',
      'org',
      'ops',
    ])
    expect(groups[3][1]).toEqual([
      'ops-channels',
      'ops-overview',
      'ops-rules',
      'admin-users',
      'admin-settings',
    ])
    expect(groups[2][1]).toHaveLength(7)
  })

  it('treats a missing user as no access beyond the workbench', () => {
    expect(ids(undefined)).toEqual([['workspace', WORKBENCH], PERSONAL])
  })
})

describe('userAccess', () => {
  it('maps role and tenant_role like the backend', () => {
    expect(userAccess({ role: 10 })).toMatchObject({
      isPlatformStaff: true,
      isTenantAdmin: true,
    })
    expect(
      userAccess({ role: 1, tenant_role: 'admin', is_payer: true })
    ).toMatchObject({
      isPlatformStaff: false,
      isTenantAdmin: true,
      isPayer: true,
    })
    expect(userAccess({ role: 1, tenant_role: 'dept_lead' })).toMatchObject({
      isTenantAdmin: false,
      isDeptLead: true,
    })
    expect(userAccess({ role: 1 }).tenantRole).toBe('')
  })
})
