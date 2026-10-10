import axios, { type AxiosRequestConfig } from 'axios'

import { ApiError } from '@/lib/api-error'
import { redirectToLogin } from '@/lib/auth'

export { ApiError, isApiError } from '@/lib/api-error'

declare module 'axios' {
  export interface AxiosRequestConfig {
    /** Do not send the browser to the sign-in page on 401. */
    skipAuthRedirect?: boolean
  }
}

/**
 * "This session's own tenant". The server resolves `~` from the session
 * (middleware.TenantSlugGuard), so the console never stores or sends a slug.
 */
export const SELF_TENANT = '~'

/** Envelope every newhub JSON handler answers with. */
export interface ApiEnvelope<T> {
  success: boolean
  message?: string
  error_code?: string
  data?: T
}

/** List payload shape of the v2 handlers (`items,total,page,page_size`). */
export interface Page<T> {
  items: T[]
  total: number
  page: number
  page_size: number
}

export const http = axios.create({
  baseURL: '',
  withCredentials: true,
  headers: { 'Cache-Control': 'no-store' },
})

function isEnvelope(value: unknown): value is ApiEnvelope<unknown> {
  return (
    typeof value === 'object' &&
    value !== null &&
    typeof (value as { success?: unknown }).success === 'boolean'
  )
}

/** Fold any axios failure into an ApiError carrying the server's message. */
export function toApiError(error: unknown): ApiError {
  if (error instanceof ApiError) return error
  if (axios.isAxiosError(error)) {
    const body: unknown = error.response?.data
    const envelope = isEnvelope(body) ? body : undefined
    const message =
      (envelope?.message && envelope.message.trim()) ||
      error.message ||
      'Request failed'
    return new ApiError(message, {
      status: error.response?.status,
      code: envelope?.error_code,
      cause: error,
    })
  }
  return new ApiError(
    error instanceof Error ? error.message : 'Request failed',
    { cause: error }
  )
}

http.interceptors.response.use(
  (response) => response,
  (error: unknown) => {
    const apiError = toApiError(error)
    const config = axios.isAxiosError(error) ? error.config : undefined
    if (apiError.status === 401 && !config?.skipAuthRedirect) {
      redirectToLogin()
    }
    return Promise.reject(apiError)
  }
)

/**
 * Open the envelope: return `data` of a successful response, throw ApiError
 * for `{success:false}` (HTTP 200 business failures included).
 */
export function unwrap<T>(body: unknown, status?: number): T {
  if (!isEnvelope(body)) {
    throw new ApiError('Unexpected response from server', { status })
  }
  if (!body.success) {
    throw new ApiError(body.message?.trim() || 'Request failed', {
      status,
      code: body.error_code,
    })
  }
  return body.data as T
}

async function send<T>(config: AxiosRequestConfig): Promise<T> {
  const res = await http.request<unknown>(config)
  return unwrap<T>(res.data, res.status)
}

/** Plain (non-tenant) endpoints such as /api/status. */
export const api = {
  get: <T>(url: string, config?: AxiosRequestConfig) =>
    send<T>({ ...config, method: 'GET', url }),
  post: <T>(url: string, data?: unknown, config?: AxiosRequestConfig) =>
    send<T>({ ...config, method: 'POST', url, data }),
  put: <T>(url: string, data?: unknown, config?: AxiosRequestConfig) =>
    send<T>({ ...config, method: 'PUT', url, data }),
  delete: <T>(url: string, config?: AxiosRequestConfig) =>
    send<T>({ ...config, method: 'DELETE', url }),
}

/** `/api/v2/~/<path>` for a tenant-scoped resource. */
export function tenantUrl(path: string): string {
  const clean = path.startsWith('/') ? path : `/${path}`
  return `/api/v2/${SELF_TENANT}${clean}`
}

/** Tenant-scoped endpoints: every call goes through `/api/v2/~/...`. */
export const tenantApi = {
  get: <T>(path: string, config?: AxiosRequestConfig) =>
    api.get<T>(tenantUrl(path), config),
  post: <T>(path: string, data?: unknown, config?: AxiosRequestConfig) =>
    api.post<T>(tenantUrl(path), data, config),
  put: <T>(path: string, data?: unknown, config?: AxiosRequestConfig) =>
    api.put<T>(tenantUrl(path), data, config),
  delete: <T>(path: string, config?: AxiosRequestConfig) =>
    api.delete<T>(tenantUrl(path), config),
}
