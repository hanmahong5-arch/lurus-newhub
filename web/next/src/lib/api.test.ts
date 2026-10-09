import {
  AxiosError,
  type AxiosAdapter,
  type InternalAxiosRequestConfig,
} from 'axios'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { ApiError, api, http, tenantApi, tenantUrl, unwrap } from './api'
import { resetRedirectGuard } from './auth'

interface Reply {
  status: number
  body: unknown
}

const seen: InternalAxiosRequestConfig[] = []
let reply: Reply = { status: 200, body: { success: true, data: null } }
const originalAdapter = http.defaults.adapter

const fakeAdapter: AxiosAdapter = async (config) => {
  seen.push(config)
  const response = {
    data: reply.body,
    status: reply.status,
    statusText: String(reply.status),
    headers: {},
    config,
  }
  if (reply.status >= 400) {
    throw new AxiosError(
      `Request failed with status code ${reply.status}`,
      'ERR_BAD_REQUEST',
      config,
      null,
      response
    )
  }
  return response
}

const assign = vi.fn()

beforeEach(() => {
  seen.length = 0
  reply = { status: 200, body: { success: true, data: null } }
  http.defaults.adapter = fakeAdapter
  resetRedirectGuard()
  assign.mockReset()
  vi.stubGlobal('location', {
    pathname: '/next/keys',
    search: '?p=2',
    hash: '',
    assign,
  })
})

afterEach(() => {
  http.defaults.adapter = originalAdapter
  vi.unstubAllGlobals()
})

describe('tenantUrl', () => {
  it('always addresses the session tenant with the ~ alias, never a slug', () => {
    expect(tenantUrl('/tokens')).toBe('/api/v2/~/tokens')
    expect(tenantUrl('tokens')).toBe('/api/v2/~/tokens')
  })
})

describe('unwrap', () => {
  it('returns data of a successful envelope', () => {
    expect(unwrap<{ a: number }>({ success: true, data: { a: 1 } })).toEqual({
      a: 1,
    })
  })

  it('throws ApiError carrying message and error_code for success:false', () => {
    try {
      unwrap({ success: false, message: 'nope', error_code: 'X_CODE' }, 200)
      expect.unreachable()
    } catch (e) {
      expect(e).toBeInstanceOf(ApiError)
      expect(e).toMatchObject({ message: 'nope', code: 'X_CODE', status: 200 })
    }
  })

  it('rejects a body that is not an envelope', () => {
    expect(() => unwrap('<html></html>', 200)).toThrow(ApiError)
    expect(() => unwrap(null)).toThrow(ApiError)
  })
})

describe('api / tenantApi', () => {
  it('sends credentials with same-origin relative URLs', async () => {
    await api.get('/api/status')
    expect(seen[0].url).toBe('/api/status')
    expect(seen[0].baseURL).toBe('')
    expect(http.defaults.withCredentials).toBe(true)
  })

  it('resolves tenant calls under /api/v2/~ and unwraps data', async () => {
    reply = { status: 200, body: { success: true, data: { id: 7 } } }
    const user = await tenantApi.get<{ id: number }>('/user/me')
    expect(user).toEqual({ id: 7 })
    expect(seen[0].url).toBe('/api/v2/~/user/me')
    expect(seen[0].method).toBe('get')
  })

  it('passes query params and bodies through', async () => {
    await tenantApi.get('/tokens', { params: { p: 2, size: 20 } })
    await tenantApi.post('/tokens', { name: 'k' })
    expect(seen[0].params).toEqual({ p: 2, size: 20 })
    expect(seen[1].method).toBe('post')
    expect(JSON.parse(String(seen[1].data))).toEqual({ name: 'k' })
  })

  it('turns an HTTP 200 success:false into a rejection', async () => {
    reply = { status: 200, body: { success: false, message: 'denied' } }
    await expect(tenantApi.get('/tokens')).rejects.toMatchObject({
      name: 'ApiError',
      message: 'denied',
    })
    expect(assign).not.toHaveBeenCalled()
  })

  it('maps an error status to ApiError with the server message and code', async () => {
    reply = {
      status: 403,
      body: {
        success: false,
        message: 'forbidden',
        error_code: 'PERMISSION_DENIED',
      },
    }
    await expect(api.get('/api/x')).rejects.toMatchObject({
      status: 403,
      code: 'PERMISSION_DENIED',
      message: 'forbidden',
    })
    expect(assign).not.toHaveBeenCalled()
  })
})

describe('401 handling', () => {
  it('sends the browser to /login with the current /next path as redirect', async () => {
    reply = { status: 401, body: { success: false, message: 'Not logged in' } }
    await expect(tenantApi.get('/user/me')).rejects.toMatchObject({
      status: 401,
    })
    expect(assign).toHaveBeenCalledTimes(1)
    expect(assign).toHaveBeenCalledWith(
      `/login?redirect=${encodeURIComponent('/next/keys?p=2')}`
    )
  })

  it('redirects once for a burst of parallel 401s', async () => {
    reply = { status: 401, body: { success: false } }
    await Promise.allSettled([api.get('/a'), api.get('/b'), api.get('/c')])
    expect(assign).toHaveBeenCalledTimes(1)
  })

  it('drops the legacy durable user hint so /login honours redirect', async () => {
    window.localStorage.setItem('user', '{"id":1}')
    reply = { status: 401, body: { success: false } }
    await expect(api.get('/a')).rejects.toBeInstanceOf(ApiError)
    expect(window.localStorage.getItem('user')).toBeNull()
  })

  it('does not redirect when the caller opts out', async () => {
    reply = { status: 401, body: { success: false } }
    await expect(
      api.get('/api/status', { skipAuthRedirect: true })
    ).rejects.toMatchObject({ status: 401 })
    expect(assign).not.toHaveBeenCalled()
  })

  it('does not loop when already on a sign-in route', async () => {
    vi.stubGlobal('location', {
      pathname: '/login',
      search: '',
      hash: '',
      assign,
    })
    reply = { status: 401, body: { success: false } }
    await expect(api.get('/a')).rejects.toBeInstanceOf(ApiError)
    expect(assign).not.toHaveBeenCalled()
  })
})
