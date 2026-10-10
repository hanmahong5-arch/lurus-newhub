import { describe, expect, it } from 'vitest'

import { NAV_ITEMS, visibleNavItems } from './nav'

describe('visibleNavItems', () => {
  it('shows all four pages to a plain signed-in user, models included', () => {
    expect(visibleNavItems(1).map((i) => i.id)).toEqual([
      'dashboard',
      'keys',
      'usage-logs',
      'models',
    ])
  })

  it('hides minRole entries below the role and shows them at or above it', () => {
    const items = [
      ...NAV_ITEMS,
      { ...NAV_ITEMS[0], id: 'admin-only', minRole: 10 },
      { ...NAV_ITEMS[0], id: 'root-only', minRole: 100 },
    ]
    const ids = (role: number | undefined) =>
      visibleNavItems(role, items).map((i) => i.id)
    expect(ids(1)).not.toContain('admin-only')
    expect(ids(10)).toContain('admin-only')
    expect(ids(10)).not.toContain('root-only')
    expect(ids(100)).toContain('root-only')
    expect(ids(undefined)).not.toContain('admin-only')
  })
})
