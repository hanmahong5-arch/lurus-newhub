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

import { parseHeaderNav } from './header-nav'
import { safeLink } from './use-site-info'
import { classifyContent } from './content'

describe('parseHeaderNav', () => {
  it('enables everything when the option is missing or broken', () => {
    for (const raw of [undefined, null, '', '   ', 'not json', '[]', '7']) {
      expect(parseHeaderNav(raw)).toEqual({
        pricing: true,
        pricingRequiresAuth: false,
        about: true,
        docs: true,
      })
    }
  })

  it('reads the object form of pricing', () => {
    const nav = parseHeaderNav(
      JSON.stringify({
        pricing: { enabled: false, requireAuth: true },
        about: false,
        docs: true,
      })
    )
    expect(nav).toEqual({
      pricing: false,
      pricingRequiresAuth: true,
      about: false,
      docs: true,
    })
  })

  it('reads the legacy boolean form of pricing', () => {
    expect(parseHeaderNav('{"pricing":false}').pricing).toBe(false)
    expect(parseHeaderNav('{"pricing":true}').pricingRequiresAuth).toBe(false)
  })
})

describe('safeLink', () => {
  it('allows http(s) URLs and same-site paths only', () => {
    expect(safeLink('https://docs.example/start')).toBe(
      'https://docs.example/start'
    )
    expect(safeLink('/docs')).toBe('/docs')
    expect(safeLink('javascript:alert(1)')).toBe('')
    expect(safeLink('data:text/html,x')).toBe('')
    expect(safeLink('//evil.example')).toBe('')
    expect(safeLink('')).toBe('')
  })
})

describe('classifyContent', () => {
  it('tells a link, markup and markdown apart', () => {
    expect(classifyContent(' https://example.com/home ')).toBe('url')
    expect(classifyContent('<h1>Hi</h1>')).toBe('html')
    expect(classifyContent('# Hi\n\nplain *text*')).toBe('markdown')
  })
})
