import { describe, expect, it } from 'vitest'

import { NAV_GROUPS } from './nav'
import { activeNavItemId, pathMatches } from './nav-active'

const ALL = NAV_GROUPS.flatMap((g) => g.items)

describe('pathMatches', () => {
  it('matches the path itself and anything below it', () => {
    expect(pathMatches('/keys', '/keys')).toBe(true)
    expect(pathMatches('/keys/123', '/keys')).toBe(true)
    expect(pathMatches('/keys/', '/keys')).toBe(true)
  })

  it('does not match a sibling that merely shares a prefix', () => {
    expect(pathMatches('/keys-batch', '/keys')).toBe(false)
    expect(pathMatches('/org/keys-batch', '/org/keys')).toBe(false)
  })
})

describe('activeNavItemId', () => {
  it('lights only the longest match, not every prefix', () => {
    expect(activeNavItemId('/usage-logs/audit', ALL)).toBe('org-audit')
    expect(activeNavItemId('/usage-logs', ALL)).toBe('usage-logs')
  })

  it('keeps a parent entry active on its sub-pages', () => {
    expect(activeNavItemId('/usage-logs/some/detail', ALL)).toBe('usage-logs')
  })

  it('is null on a page the sidebar does not list', () => {
    expect(activeNavItemId('/pricing', ALL)).toBeNull()
  })
})
