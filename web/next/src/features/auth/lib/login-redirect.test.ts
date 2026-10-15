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
import { afterEach, describe, expect, it } from 'vitest'

import {
  DEFAULT_LANDING,
  postLoginUrl,
  readRedirectParam,
  safeRedirectPath,
} from './login-redirect'

describe('safeRedirectPath', () => {
  it('accepts in-site relative paths with query and hash', () => {
    expect(safeRedirectPath('/keys')).toBe('/keys')
    expect(safeRedirectPath('/usage-logs?p=2#top')).toBe('/usage-logs?p=2#top')
    expect(safeRedirectPath('/console/v2/token')).toBe('/console/v2/token')
  })

  it.each([
    ['absolute url', 'https://evil.example/next'],
    ['protocol-relative', '//evil.example/next'],
    ['backslash host', '/\\evil.example'],
    ['embedded backslash', '/next\\..\\x'],
    ['no leading slash', 'next/keys'],
    ['javascript scheme', 'javascript:alert(1)'],
    ['control character', '/next/\u0000keys'],
    ['newline', '/next/\nkeys'],
    ['login loop', '/login?redirect=/next/'],
    ['login with a slug', '/login/acme'],
    ['bridge loop', '/bridge-login'],
    ['register loop', '/register'],
    ['empty', ''],
    ['non-string', null],
    ['too long', `/${'a'.repeat(2048)}`],
  ])('rejects %s', (_name, raw) => {
    expect(safeRedirectPath(raw)).toBeNull()
  })

  it('does not mistake a longer first segment for a sign-in route', () => {
    expect(safeRedirectPath('/loginx')).toBe('/loginx')
    expect(safeRedirectPath('/registered')).toBe('/registered')
  })
})

describe('readRedirectParam / postLoginUrl', () => {
  afterEach(() => {
    window.history.replaceState(null, '', '/')
  })

  it('reads and validates ?redirect=', () => {
    expect(readRedirectParam('?redirect=%2Fkeys')).toBe('/keys')
    expect(
      readRedirectParam('?redirect=https%3A%2F%2Fevil.example')
    ).toBeNull()
    expect(readRedirectParam('')).toBeNull()
  })

  it('falls back to the default landing page', () => {
    // The console owns the site root; signing in lands on its dashboard.
    expect(DEFAULT_LANDING).toBe('/dashboard')
    window.history.replaceState(null, '', '/login')
    expect(postLoginUrl('https://hub.example')).toBe(
      `https://hub.example${DEFAULT_LANDING}`
    )
    window.history.replaceState(null, '', '/login?redirect=%2Fmodels')
    expect(postLoginUrl('https://hub.example')).toBe(
      'https://hub.example/models'
    )
  })

  it('ignores an unsafe ?redirect= on the current URL', () => {
    window.history.replaceState(
      null,
      '',
      '/login?redirect=https%3A%2F%2Fevil.example'
    )
    expect(postLoginUrl('https://hub.example')).toBe(
      `https://hub.example${DEFAULT_LANDING}`
    )
  })
})
