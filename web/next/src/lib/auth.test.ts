import { describe, expect, it } from 'vitest'

import { ROLE, buildLoginUrl, hasMinRole } from './auth'

describe('auth helpers', () => {
  it('builds the legacy login URL with an encoded return path', () => {
    expect(buildLoginUrl('/next/usage-logs?x=1#a')).toBe(
      '/login?redirect=%2Fnext%2Fusage-logs%3Fx%3D1%23a'
    )
  })

  it('gates on role numerically and fails closed on a missing role', () => {
    expect(hasMinRole(ROLE.admin, ROLE.admin)).toBe(true)
    expect(hasMinRole(ROLE.user, ROLE.admin)).toBe(false)
    expect(hasMinRole(undefined, ROLE.admin)).toBe(false)
    expect(hasMinRole(ROLE.root, ROLE.admin)).toBe(true)
  })
})
