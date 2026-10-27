import { beforeEach, describe, expect, it, vi } from 'vitest'

import { ApiError } from '@/lib/api'

import {
  batchUpdateChannelStatus,
  copyChannel,
  getChannels,
  requireServerSuccess,
  updateChannelStatus,
} from './api'

const { request } = vi.hoisted(() => ({ request: vi.fn() }))

vi.mock('@/lib/api', async (orig) => {
  const actual = await orig<typeof import('@/lib/api')>()
  return { ...actual, http: { request } }
})

beforeEach(() => {
  vi.clearAllMocks()
})

describe('admin channels v2 requests', () => {
  it('lists without a trailing slash and returns the envelope untouched', async () => {
    request.mockResolvedValue({
      data: { success: false, message: 'denied' },
    })
    const res = await getChannels({ p: 2, page_size: 10, tag_mode: true })
    expect(request).toHaveBeenCalledWith({
      method: 'GET',
      url: '/api/v2/admin/channels',
      params: { p: 2, page_size: 10, tag_mode: true },
    })
    // A 200 with success:false is the caller's to inspect, not swallowed.
    expect(res).toEqual({ success: false, message: 'denied' })
  })

  it('implements enable/disable as a partial update (there is no status endpoint)', async () => {
    request.mockResolvedValue({ data: { success: true } })
    await updateChannelStatus(5, 2)
    expect(request).toHaveBeenCalledWith({
      method: 'PUT',
      url: '/api/v2/admin/channels',
      data: { id: 5, status: 2 },
    })
  })

  it('passes copy options as query params', async () => {
    request.mockResolvedValue({ data: { success: true, data: { id: 9 } } })
    await copyChannel(4, { suffix: '_x', reset_balance: false })
    expect(request).toHaveBeenCalledWith({
      method: 'POST',
      url: '/api/v2/admin/channels/copy/4',
      params: { suffix: '_x', reset_balance: false },
    })
  })
})

describe('batch status', () => {
  it('counts the updates that succeeded', async () => {
    request
      .mockResolvedValueOnce({ data: { success: true } })
      .mockResolvedValueOnce({ data: { success: false, message: 'no' } })
      .mockResolvedValueOnce({ data: { success: true } })
    const res = await batchUpdateChannelStatus([1, 2, 3], 1)
    expect(res).toEqual({ success: true, data: 2 })
  })

  it('fails with the first server message when nothing succeeded', async () => {
    request.mockResolvedValue({ data: { success: false, message: 'refused' } })
    const res = await batchUpdateChannelStatus([1, 2], 2)
    expect(res).toEqual({ success: false, message: 'refused', data: 0 })
  })

  it('treats a rejected request as a failure with its own message', async () => {
    request.mockRejectedValue(new ApiError('forbidden', { status: 403 }))
    const res = await batchUpdateChannelStatus([1], 2)
    expect(res).toEqual({ success: false, message: 'forbidden', data: 0 })
  })
})

describe('requireServerSuccess', () => {
  it('returns a successful envelope', () => {
    const body = { success: true, data: 1 }
    expect(requireServerSuccess(body)).toBe(body)
  })

  it('throws the server message and code', () => {
    try {
      requireServerSuccess({
        success: false,
        message: ' bad ',
        error_code: 'E_X',
      })
      expect.unreachable()
    } catch (e) {
      expect(e).toBeInstanceOf(ApiError)
      expect((e as ApiError).message).toBe('bad')
      expect((e as ApiError).code).toBe('E_X')
    }
  })

  it('falls back when the server gave no message', () => {
    expect(() => requireServerSuccess({ success: false }, 'fallback')).toThrow(
      'fallback'
    )
  })
})
