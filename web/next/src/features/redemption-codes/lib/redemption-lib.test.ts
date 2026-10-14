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
import { describe, expect, it, vi } from 'vitest'

import { ApiError } from '@/lib/api'

import { deleteMany } from './bulk-delete'
import { buildExportContent } from './export'
import {
  byteLength,
  isMaskedKey,
  isRedemptionExpired,
  isTimestampExpired,
} from './utils'

const NOW = 1_800_000_000_000

describe('expiry', () => {
  it('treats 0 as never and compares seconds to now', () => {
    expect(isTimestampExpired(0, NOW)).toBe(false)
    expect(isTimestampExpired(1_799_999_999, NOW)).toBe(true)
    expect(isTimestampExpired(1_800_000_001, NOW)).toBe(false)
  })

  it('only an unused code can be expired', () => {
    const past = 1_700_000_000
    expect(isRedemptionExpired({ status: 1, expired_time: past }, NOW)).toBe(true)
    expect(isRedemptionExpired({ status: 3, expired_time: past }, NOW)).toBe(false)
    expect(isRedemptionExpired({ status: 2, expired_time: past }, NOW)).toBe(false)
  })
})

describe('keys', () => {
  it('recognises the masked form and counts bytes, not characters', () => {
    expect(isMaskedKey('abcd********wxyz')).toBe(true)
    expect(isMaskedKey('k'.repeat(32))).toBe(false)
    expect(byteLength('abc')).toBe(3)
    expect(byteLength('兑换码')).toBe(9)
  })
})

describe('buildExportContent', () => {
  const base = {
    keys: ['code-1', 'code-2'],
    name: 'launch',
    quota: '$10.00',
    includeName: true,
    includeQuota: true,
    labels: { name: 'Name', code: 'Code', quota: 'Quota' },
  }

  it('writes tab-separated rows for TXT', () => {
    expect(buildExportContent({ ...base, format: 'txt' })).toBe(
      'launch\tcode-1\t$10.00\nlaunch\tcode-2\t$10.00\n'
    )
  })

  it('writes an escaped pipe table for Markdown and honours the column choices', () => {
    const md = buildExportContent({
      ...base,
      name: 'a|b',
      includeQuota: false,
      format: 'md',
    })
    expect(md).toBe(
      '| Name | Code |\n| --- | --- |\n| a\\|b | code-1 |\n| a\\|b | code-2 |\n'
    )
  })

  it('flattens line breaks inside a value so a row stays one line', () => {
    const txt = buildExportContent({ ...base, name: 'a\nb', format: 'txt' })
    expect(txt.split('\n')[0]).toBe('a b\tcode-1\t$10.00')
  })
})

describe('deleteMany', () => {
  it('keeps going past a failure and reports both sides', async () => {
    const remove = vi.fn((id: number) =>
      id === 2 ? Promise.reject(new ApiError('Access denied')) : Promise.resolve()
    )
    const result = await deleteMany([1, 2, 3], remove, 2)
    expect(remove).toHaveBeenCalledTimes(3)
    expect(result.deleted.sort()).toEqual([1, 3])
    expect(result.failed).toEqual([{ id: 2, message: 'Access denied' }])
  })

  it('does nothing for an empty selection', async () => {
    const remove = vi.fn()
    expect(await deleteMany([], remove)).toEqual({ deleted: [], failed: [] })
    expect(remove).not.toHaveBeenCalled()
  })

  it('never runs more requests at once than asked', async () => {
    let running = 0
    let peak = 0
    const remove = async () => {
      running += 1
      peak = Math.max(peak, running)
      await new Promise((r) => setTimeout(r, 5))
      running -= 1
    }
    await deleteMany([1, 2, 3, 4, 5, 6], remove, 2)
    expect(peak).toBe(2)
  })
})
