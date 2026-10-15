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

import { ApiError } from '@/lib/api'

import {
  createChannel,
  createToken,
  fetchUpstreamModels,
  testChannel,
  updateChannelModels,
} from './api'
import { MISSING_CHANNEL_ID, MISSING_KEY } from './types'

const { get, post, put, httpPost } = vi.hoisted(() => ({
  get: vi.fn(),
  post: vi.fn(),
  put: vi.fn(),
  httpPost: vi.fn(),
}))

vi.mock('@/lib/api', async (orig) => {
  const actual = await orig<typeof import('@/lib/api')>()
  return {
    ...actual,
    tenantApi: { get, post, put, delete: vi.fn() },
    http: { post: httpPost },
  }
})

beforeEach(() => vi.clearAllMocks())

describe('channel api', () => {
  it('createChannel posts to the tenant channels collection and maps id + name', async () => {
    post.mockResolvedValueOnce({ id: 42, name: 'x' })
    const body = { name: 'n', type: 1, base_url: '', key: 'k', models: 'a' }
    await expect(createChannel(body)).resolves.toEqual({ id: 42, name: 'x' })
    expect(post).toHaveBeenCalledWith('/channels', body)
  })

  it('createChannel falls back to the sent name and rejects a missing id', async () => {
    const body = { name: 'sent', type: 1, base_url: '', key: 'k', models: 'a' }
    post.mockResolvedValueOnce({ id: 3 })
    await expect(createChannel(body)).resolves.toEqual({ id: 3, name: 'sent' })
    for (const bad of [{}, { id: 0 }, { id: '7' }, null, 'ok']) {
      post.mockResolvedValueOnce(bad)
      await expect(createChannel(body)).rejects.toMatchObject({
        code: MISSING_CHANNEL_ID,
      })
    }
  })

  it('fetchUpstreamModels maps the diff defensively', async () => {
    get.mockResolvedValueOnce({
      upstream: ['a', '', 5, 'b'],
      new: ['b'],
      missing: 'nope',
    })
    await expect(fetchUpstreamModels(9)).resolves.toEqual({
      upstream: ['a', 'b'],
      new: ['b'],
      missing: [],
    })
    expect(get).toHaveBeenCalledWith('/channels/9/upstream-models')
    get.mockResolvedValueOnce(null)
    await expect(fetchUpstreamModels(9)).resolves.toEqual({
      upstream: [],
      new: [],
      missing: [],
    })
  })

  it('updateChannelModels sends only the model list', async () => {
    put.mockResolvedValueOnce({})
    await updateChannelModels(9, 'a,b')
    expect(put).toHaveBeenCalledWith('/channels/9', { models: 'a,b' })
  })

  it('testChannel reads the TOP-LEVEL result and does not go through the envelope', async () => {
    httpPost.mockResolvedValueOnce({
      data: { success: true, latency_ms: 31 },
    })
    await expect(testChannel(9)).resolves.toEqual({
      success: true,
      latencyMs: 31,
      error: '',
    })
    expect(httpPost).toHaveBeenCalledWith('/api/v2/~/channels/9/test', {})
    expect(post).not.toHaveBeenCalled()

    httpPost.mockResolvedValueOnce({
      data: { success: false, latency_ms: 5, error: 'boom' },
    })
    await expect(testChannel(9)).resolves.toEqual({
      success: false,
      latencyMs: 5,
      error: 'boom',
    })
  })

  it('testChannel treats a malformed body as a failure, never a success', async () => {
    for (const data of [undefined, null, 'ok', {}, { success: 'true' }]) {
      httpPost.mockResolvedValueOnce({ data })
      await expect(testChannel(1)).resolves.toMatchObject({ success: false })
    }
  })

  it('testChannel lets a transport failure reject', async () => {
    httpPost.mockRejectedValueOnce(new ApiError('Network Error'))
    await expect(testChannel(1)).rejects.toThrow('Network Error')
  })
})

describe('token api', () => {
  const body = {
    name: 'k',
    group: 'default',
    unlimited_quota: true,
    remain_quota: 0,
    expired_time: -1,
  }

  it('createToken returns the plaintext key from the answer', async () => {
    post.mockResolvedValueOnce({ id: 4, name: 'k', key: 'sk-1' })
    await expect(createToken(body)).resolves.toEqual({
      id: 4,
      name: 'k',
      key: 'sk-1',
    })
    expect(post).toHaveBeenCalledWith('/tokens', body)
  })

  it('createToken refuses an answer with no key', async () => {
    for (const bad of [{ id: 1 }, { key: '' }, { key: 5 }, null]) {
      post.mockResolvedValueOnce(bad)
      await expect(createToken(body)).rejects.toMatchObject({
        code: MISSING_KEY,
      })
    }
  })
})
