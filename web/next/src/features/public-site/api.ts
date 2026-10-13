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
import { ApiError, api, http } from '@/lib/api'

// Every request on the public site opts out of the 401 -> sign-in redirect in
// lib/api.ts: these pages are for people without a session, and a single
// stray 401 must never bounce them away from the page they came to read.
const QUIET = { skipAuthRedirect: true } as const

/** Trimmed text of an option-backed document; null and non-strings are ''. */
function text(value: unknown): string {
  return typeof value === 'string' ? value.trim() : ''
}

/** GET /api/home_page_content: markdown, HTML or a URL; '' = built-in landing. */
export async function fetchHomePageContent(): Promise<string> {
  return text(await api.get<unknown>('/api/home_page_content', QUIET))
}

/** GET /api/about: markdown, HTML or a URL; '' = built-in text. */
export async function fetchAboutContent(): Promise<string> {
  return text(await api.get<unknown>('/api/about', QUIET))
}

export type LegalKind = 'user-agreement' | 'privacy-policy'

/** GET /api/user-agreement and /api/privacy-policy. */
export async function fetchLegalDocument(kind: LegalKind): Promise<string> {
  return text(await api.get<unknown>(`/api/${kind}`, QUIET))
}

/** One row of GET /api/pricing (entity.Pricing). Optional = may be absent. */
export interface PublicPricingRow {
  model_name: string
  description?: string
  icon?: string
  tags?: string
  vendor_id?: number
  quota_type?: number
  model_ratio?: number | null
  model_price?: number | null
  owner_by?: string
  completion_ratio?: number | null
  enable_groups?: string[] | null
  supported_endpoint_types?: string[] | null
  modality?: string | null
  usage_unit?: string | null
  search_unit_price?: number | null
  price_configured?: boolean
  /** Not sent by this server; read only if a future one adds them. */
  cache_ratio?: number | null
}

export interface PublicVendor {
  id: number
  name: string
  description?: string
  icon?: string
}

export interface EndpointInfo {
  path?: string
  method?: string
}

/** The whole body of GET /api/pricing: the rows plus their lookup tables. */
export interface PublicPricingPayload {
  rows: PublicPricingRow[]
  vendors: PublicVendor[]
  groupRatio: Record<string, number>
  /** group -> description, for the groups an anonymous caller may use. */
  usableGroup: Record<string, string>
  endpoints: Record<string, EndpointInfo>
}

function asRecord<T>(value: unknown): Record<string, T> {
  return typeof value === 'object' && value !== null && !Array.isArray(value)
    ? (value as Record<string, T>)
    : {}
}

/**
 * GET /api/pricing. The handler answers `{success, data: rows, vendors,
 * group_ratio, usable_group, supported_endpoint, ...}`, so the lookup tables
 * sit beside `data` and the shared envelope helper (which opens only `data`)
 * cannot be used.
 */
export async function fetchPublicPricing(): Promise<PublicPricingPayload> {
  const res = await http.get<unknown>('/api/pricing', QUIET)
  const body = res.data as Record<string, unknown> | null
  if (!body || typeof body !== 'object' || body.success !== true) {
    const message =
      body && typeof body.message === 'string' && body.message.trim()
        ? body.message.trim()
        : 'Failed to load the price list'
    throw new ApiError(message, { status: res.status })
  }
  return {
    rows: Array.isArray(body.data) ? (body.data as PublicPricingRow[]) : [],
    vendors: Array.isArray(body.vendors) ? (body.vendors as PublicVendor[]) : [],
    groupRatio: asRecord<number>(body.group_ratio),
    usableGroup: asRecord<string>(body.usable_group),
    endpoints: asRecord<EndpointInfo>(body.supported_endpoint),
  }
}

export type ModelHealth = 'operational' | 'degraded' | 'down'

export interface ModelStatusPayload {
  models: Array<{ model: string; status: string }>
  updated_at?: number
}

/** GET /api/v2/public/model-status: anonymous, cached 30s server-side. */
export async function fetchModelStatus(): Promise<ModelStatusPayload> {
  const data = await api.get<ModelStatusPayload | null>(
    '/api/v2/public/model-status',
    QUIET
  )
  return {
    models: Array.isArray(data?.models) ? data.models : [],
    updated_at: data?.updated_at,
  }
}

/** Narrow the server's status string; anything unknown reads as no data. */
export function asModelHealth(value: unknown): ModelHealth | null {
  return value === 'operational' || value === 'degraded' || value === 'down'
    ? value
    : null
}
