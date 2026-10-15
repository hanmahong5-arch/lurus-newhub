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
import { beforeEach, describe, expect, it, vi } from 'vitest'

import { credentialsFromHash } from './bridge-credentials'
import { inviteLink, readInviteCode } from './invite'
import {
  buildSetupPayload,
  USERNAME_MAX_CHARS,
  usernameProblem,
} from './setup-rules'
import {
  clearTenantSlug,
  hasStoredUser,
  storeTenantSlug,
  storeUser,
} from './session-hint'

describe('credentialsFromHash', () => {
  it('parses the token and user id out of the fragment, defaulting the user to 1', () => {
    expect(credentialsFromHash('#t=abc&u=7')).toEqual({
      token: 'abc',
      userId: '7',
    })
    expect(credentialsFromHash('#t=abc')).toEqual({ token: 'abc', userId: '1' })
    expect(credentialsFromHash('')).toEqual({ token: '', userId: '1' })
    expect(credentialsFromHash(undefined)).toEqual({ token: '', userId: '1' })
  })
})

describe('invite codes', () => {
  it('builds the delivery URL the sign-in flow carries through the IdP', () => {
    expect(inviteLink('https://hub.test', 'abc123XYZ_-')).toBe(
      'https://hub.test/login?invite=abc123XYZ_-'
    )
    expect(inviteLink('https://hub.test', 'a b')).toBe(
      'https://hub.test/login?invite=a%20b'
    )
  })

  it('reads a well-formed code and drops a malformed one', () => {
    expect(readInviteCode('?invite=abc123XYZ_-')).toBe('abc123XYZ_-')
    expect(readInviteCode(`?invite=${encodeURIComponent('not valid!')}`)).toBe(
      null
    )
    expect(readInviteCode(`?invite=${'a'.repeat(65)}`)).toBeNull()
    expect(readInviteCode(`?invite=${'a'.repeat(64)}`)).toBe('a'.repeat(64))
    expect(readInviteCode('?invite=')).toBeNull()
    expect(readInviteCode('')).toBeNull()
  })
})

describe('session hints', () => {
  beforeEach(() => {
    window.localStorage.clear()
  })

  it('writes the keys other code reads', () => {
    expect(hasStoredUser()).toBe(false)
    storeUser({ id: 3, username: 'ada' })
    expect(JSON.parse(window.localStorage.getItem('user') ?? 'null')).toEqual({
      id: 3,
      username: 'ada',
    })
    expect(hasStoredUser()).toBe(true)
    storeTenantSlug('acme')
    expect(window.localStorage.getItem('tenant_slug')).toBe('acme')
    clearTenantSlug()
    expect(window.localStorage.getItem('tenant_slug')).toBeNull()
  })

  it('never overwrites a working slug with an empty or missing one', () => {
    storeTenantSlug('acme')
    storeTenantSlug('')
    storeTenantSlug(undefined)
    expect(window.localStorage.getItem('tenant_slug')).toBe('acme')
  })

  it('survives storage being denied', () => {
    const denied = vi.spyOn(Storage.prototype, 'setItem').mockImplementation(
      () => {
        throw new Error('denied')
      }
    )
    const read = vi.spyOn(Storage.prototype, 'getItem').mockImplementation(
      () => {
        throw new Error('denied')
      }
    )
    expect(() => storeUser({ id: 1 })).not.toThrow()
    expect(() => storeTenantSlug('x')).not.toThrow()
    expect(hasStoredUser()).toBe(false)
    denied.mockRestore()
    read.mockRestore()
  })
})

describe('setup rules', () => {
  it('requires a name and counts characters, not bytes', () => {
    expect(usernameProblem('')).toBe('required')
    expect(usernameProblem('   ')).toBe('required')
    expect(usernameProblem('root')).toBeNull()
    // Five CJK characters are fifteen UTF-8 bytes: still well inside 12.
    expect(usernameProblem('管理员账号员')).toBeNull()
    expect(usernameProblem('a'.repeat(USERNAME_MAX_CHARS))).toBeNull()
    expect(usernameProblem('a'.repeat(USERNAME_MAX_CHARS + 1))).toBe('too-long')
    expect(usernameProblem(` ${'a'.repeat(USERNAME_MAX_CHARS)} `)).toBeNull()
  })

  it('translates the usage mode into the two backend flags and drops the UI-only field', () => {
    expect(
      buildSetupPayload({ rootInit: false, username: ' root ', usageMode: 'demo' })
    ).toEqual({ username: 'root', SelfUseModeEnabled: false, DemoSiteEnabled: true })
    expect(
      buildSetupPayload({ rootInit: false, username: 'root', usageMode: 'self' })
    ).toEqual({ username: 'root', SelfUseModeEnabled: true, DemoSiteEnabled: false })
    const external = buildSetupPayload({
      rootInit: false,
      username: 'root',
      usageMode: 'external',
    })
    expect(external.SelfUseModeEnabled).toBe(false)
    expect(external.DemoSiteEnabled).toBe(false)
    expect(external).not.toHaveProperty('usageMode')
  })

  it('sends no username when an administrator already exists', () => {
    const payload = buildSetupPayload({
      rootInit: true,
      username: 'ignored',
      usageMode: 'self',
    })
    expect(payload).not.toHaveProperty('username')
  })
})
