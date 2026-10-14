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
  EMPTY_MJ_FILTERS,
  EMPTY_TASK_FILTERS,
  audioClips,
  durationSeconds,
  mapPage,
  mapTaskLog,
  mjParams,
  rangeError,
  safeHttpUrl,
  taskParams,
  taskResultUrl,
} from './tasks'

describe('query building', () => {
  it('tasks use p + second-based bounds', () => {
    const p = taskParams(
      {
        ...EMPTY_TASK_FILTERS,
        taskId: ' t1 ',
        status: 'SUCCESS',
        start: '2026-01-02T00:00',
      },
      3,
      50
    )
    expect(p.p).toBe(3)
    expect(p.page_size).toBe(50)
    expect(p.task_id).toBe('t1')
    expect(p.status).toBe('SUCCESS')
    expect(p.start_timestamp).toBe(
      Math.floor(new Date('2026-01-02T00:00').getTime() / 1000)
    )
    expect('platform' in p).toBe(false)
  })

  it('drawing rows use millisecond bounds', () => {
    const p = mjParams(
      { ...EMPTY_MJ_FILTERS, mjId: 'x', end: '2026-01-02T00:00' },
      1,
      20
    )
    expect(p.mj_id).toBe('x')
    expect(p.end_timestamp).toBe(new Date('2026-01-02T00:00').getTime())
  })

  it('detects an inverted window', () => {
    expect(rangeError('2026-02-01T00:00', '2026-01-01T00:00')).toBe(true)
    expect(rangeError('2026-01-01T00:00', '2026-02-01T00:00')).toBe(false)
    expect(rangeError('', '2026-02-01T00:00')).toBe(false)
  })
})

describe('row reading', () => {
  it('null items read as an empty page', () => {
    expect(mapPage({ items: null, total: 0 }, mapTaskLog)).toEqual({
      items: [],
      total: 0,
    })
  })

  it('only a successful task exposes a result link', () => {
    const ok = mapTaskLog({ status: 'SUCCESS', fail_reason: 'https://a.example/x' })
    const failed = mapTaskLog({ status: 'FAILURE', fail_reason: 'https://a.example/x' })
    expect(taskResultUrl(ok)).toBe('https://a.example/x')
    expect(taskResultUrl(failed)).toBeNull()
  })

  it('refuses non-http links', () => {
    expect(safeHttpUrl('javascript:alert(1)')).toBeNull()
    expect(safeHttpUrl('data:text/html,x')).toBeNull()
    expect(safeHttpUrl('not a url')).toBeNull()
    expect(safeHttpUrl('http://a.example/p')).toBe('http://a.example/p')
  })

  it('reads clips from an array or a JSON string and drops unsafe ones', () => {
    const clips = [
      { title: 'a', audio_url: 'https://c.example/a.mp3' },
      { title: 'b', audio_url: 'javascript:1' },
      { title: 'c' },
    ]
    expect(audioClips(clips)).toEqual([
      { title: 'a', audioUrl: 'https://c.example/a.mp3' },
    ])
    expect(audioClips(JSON.stringify(clips))).toHaveLength(1)
    expect(audioClips('{broken')).toEqual([])
  })

  it('duration needs both ends and honours the unit', () => {
    expect(durationSeconds(10, 40, 'seconds')).toBe(30)
    expect(durationSeconds(10_000, 40_000, 'milliseconds')).toBe(30)
    expect(durationSeconds(10, 0, 'seconds')).toBeNull()
    expect(durationSeconds(40, 10, 'seconds')).toBeNull()
  })
})
