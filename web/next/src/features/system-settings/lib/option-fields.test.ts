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
  baselineOf,
  comparable,
  isFieldDirty,
  prettyJson,
  serializeField,
  validateField,
  type OptionField,
} from './option-fields'

const NUMBER: OptionField = {
  kind: 'number',
  key: 'RetryTimes',
  label: 'Retry Times',
  integer: true,
  min: 0,
}
const JSON_OBJ: OptionField = {
  kind: 'json',
  key: 'GroupRatio',
  label: 'Group ratios',
  expect: 'object',
}
const LIST: OptionField = {
  kind: 'list',
  key: 'fetch_setting.domain_list',
  label: 'Domain list',
}
const TEXT: OptionField = {
  kind: 'text',
  key: 'ServerAddress',
  label: 'Server Address',
  url: true,
  trimTrailingSlash: true,
}

describe('baselineOf', () => {
  it('reads switches, defaults and write-only secrets', () => {
    const sw: OptionField = { kind: 'switch', key: 'A', label: 'A' }
    expect(baselineOf(sw, { A: 'true' })).toBe('true')
    expect(baselineOf(sw, { A: 'false' })).toBe('false')
    expect(baselineOf({ ...sw, defaultValue: true }, {})).toBe('true')
    expect(
      baselineOf({ kind: 'secret', key: 'S', label: 'S' }, { S: 'x' })
    ).toBe('')
  })

  it('pretty-prints stored JSON and falls back to the empty document', () => {
    expect(baselineOf(JSON_OBJ, { GroupRatio: '{"a":1}' })).toBe(
      '{\n  "a": 1\n}'
    )
    expect(baselineOf(JSON_OBJ, {})).toBe('{}')
    expect(prettyJson('not json', '{}')).toBe('not json')
  })

  it('turns a stored JSON array into one item per line', () => {
    expect(
      baselineOf(LIST, { 'fetch_setting.domain_list': '["a.com","b.com"]' })
    ).toBe('a.com\nb.com')
    expect(baselineOf(LIST, { 'fetch_setting.domain_list': 'null' })).toBe('')
  })
})

describe('dirty detection', () => {
  it('ignores JSON whitespace and number formatting', () => {
    expect(
      isFieldDirty(JSON_OBJ, '{ "a" : 1 }', '{\n  "a": 1\n}')
    ).toBe(false)
    expect(isFieldDirty(JSON_OBJ, '{"a":2}', '{"a":1}')).toBe(true)
    expect(isFieldDirty(NUMBER, '3.0', '3')).toBe(false)
    expect(isFieldDirty(NUMBER, undefined, '3')).toBe(false)
  })

  it('treats blank list lines and a trailing slash as no change', () => {
    expect(isFieldDirty(LIST, 'a.com\n\n b.com ', 'a.com\nb.com')).toBe(false)
    expect(isFieldDirty(TEXT, 'https://x.io/', 'https://x.io')).toBe(false)
  })

  it('a secret is dirty only when something was typed', () => {
    const secret: OptionField = { kind: 'secret', key: 'S', label: 'S' }
    expect(isFieldDirty(secret, '', '')).toBe(false)
    expect(isFieldDirty(secret, 'new', '')).toBe(true)
    expect(comparable(secret, 'new')).toBe('new')
  })
})

describe('validateField', () => {
  it('checks numbers', () => {
    expect(validateField(NUMBER, '')).toBe('Enter a number')
    expect(validateField(NUMBER, 'abc')).toBe('Please enter a valid number')
    expect(validateField(NUMBER, '1.5')).toBe('Enter a whole number')
    expect(validateField(NUMBER, '-1')).toBe('Value is below the minimum')
    expect(validateField(NUMBER, '3')).toBeNull()
    expect(
      validateField({ ...NUMBER, integer: false, positive: true }, '0')
    ).toBe('Must be greater than 0')
    expect(validateField({ ...NUMBER, max: 5 }, '6')).toBe(
      'Value is above the maximum'
    )
  })

  it('checks JSON shape', () => {
    expect(validateField(JSON_OBJ, '{')).toBe('Invalid JSON')
    expect(validateField(JSON_OBJ, '[]')).toBe('Expected a JSON object')
    expect(validateField(JSON_OBJ, '')).toBeNull()
    expect(validateField({ ...JSON_OBJ, expect: 'array' }, '{}')).toBe(
      'Expected a JSON array'
    )
    expect(
      validateField({ ...JSON_OBJ, validate: () => 'nope' }, '{"a":1}')
    ).toBe('nope')
  })

  it('checks urls, required text and list items', () => {
    expect(validateField(TEXT, 'ftp://x')).toBe('Enter an absolute http(s) URL')
    expect(validateField(TEXT, '')).toBeNull()
    expect(validateField({ ...TEXT, required: true }, '  ')).toBe(
      'This field is required'
    )
    expect(
      validateField(
        { ...LIST, validateItem: (item) => (item === 'bad' ? 'no' : null) },
        'ok\nbad'
      )
    ).toBe('no')
  })
})

describe('serializeField', () => {
  it('sends numbers as strings so the integer parser accepts them', () => {
    expect(serializeField(NUMBER, ' 5 ')).toBe('5')
    expect(serializeField({ ...NUMBER, integer: false }, '0.25')).toBe('0.25')
  })

  it('sends compact JSON, a JSON array for lists and trimmed addresses', () => {
    expect(serializeField(JSON_OBJ, '{\n "a": 1\n}')).toBe('{"a":1}')
    expect(serializeField(JSON_OBJ, '')).toBe('{}')
    expect(serializeField(LIST, 'a.com\n\nb.com')).toBe('["a.com","b.com"]')
    expect(serializeField(TEXT, 'https://x.io//')).toBe('https://x.io')
    expect(
      serializeField({ kind: 'switch', key: 'A', label: 'A' }, 'true')
    ).toBe(true)
  })
})
