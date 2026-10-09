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
import { AxiosError, type AxiosAdapter, type InternalAxiosRequestConfig } from 'axios'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { http, isApiError } from '@/lib/api'

import { fetchRealtimeLogs, fetchTrend } from './api'

let reply: { status: number; body: unknown }
const seen: InternalAxiosRequestConfig[] = []
const original = http.defaults.adapter

const fake: AxiosAdapter = async (config) => {
  seen.push(config)
  const response = {
    data: reply.body,
    status: reply.status,
    statusText: String(reply.status),
    headers: {},
    config,
  }
  if (reply.status >= 400) {
    throw new AxiosError('failed', 'ERR_BAD_REQUEST', config, null, response)
  }
  return response
}

beforeEach(() => {
  seen.length = 0
  http.defaults.adapter = fake
  vi.stubGlobal('location', {
    pathname: '/next/dashboard',
    search: '',
    hash: '',
    assign: vi.fn(),
  })
})
afterEach(() => {
  http.defaults.adapter = original
  vi.unstubAllGlobals()
})

describe('fetchRealtimeLogs', () => {
  it('asks the self-tenant logs route for the 5 minute window, page_size 100', async () => {
    reply = {
      status: 200,
      body: { success: true, data: { logs: [{ id: 1 }], total: 321 } },
    }
    const out = await fetchRealtimeLogs(10_000)
    expect(seen[0].url).toBe('/api/v2/~/logs')
    expect(seen[0].params).toEqual({
      page: 1,
      page_size: 100,
      start_time: 9_700,
    })
    expect(out).toEqual({ logs: [{ id: 1 }], total: 321 })
  })
  it('a null body is an honest empty window', async () => {
    reply = { status: 200, body: { success: true, data: null } }
    expect(await fetchRealtimeLogs(10_000)).toEqual({ logs: [], total: null })
  })
  it('a 500 rejects instead of returning an empty list', async () => {
    reply = {
      status: 500,
      body: { success: false, message: 'Failed to retrieve logs' },
    }
    await expect(fetchRealtimeLogs()).rejects.toSatisfy(isApiError)
  })
  it('a 200 success:false rejects', async () => {
    reply = { status: 200, body: { success: false, message: 'nope' } }
    await expect(fetchRealtimeLogs()).rejects.toThrow('nope')
  })
})

describe('fetchTrend', () => {
  it('requests a span under the 30 day server ceiling', async () => {
    reply = {
      status: 200,
      body: { success: true, data: [{ model_name: 'a', quota: 1 }] },
    }
    const out = await fetchTrend(3_000_000)
    const p = seen[0].params as {
      start_timestamp: number
      end_timestamp: number
    }
    expect(seen[0].url).toBe('/api/data/self/')
    expect(p.end_timestamp - p.start_timestamp).toBeLessThan(2_592_000)
    expect(out.rows).toHaveLength(1)
    expect(out.end).toBe(3_000_000)
  })
  it('null data -> empty rows; success:false (span refusal) -> rejects', async () => {
    reply = { status: 200, body: { success: true, data: null } }
    expect((await fetchTrend()).rows).toEqual([])
    reply = { status: 200, body: { success: false, message: 'span too long' } }
    await expect(fetchTrend()).rejects.toThrow('span too long')
  })
})
