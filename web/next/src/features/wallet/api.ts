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
import { api, tenantApi } from '@/lib/api'

import type {
  CheckoutOrder,
  CheckoutStatus,
  PagedList,
  PaymentMethod,
  TopupRecord,
  WalletInfo,
  WalletTransaction,
} from './types'

export const walletKeys = {
  info: ['wallet', 'info'] as const,
  methods: ['wallet', 'payment-methods'] as const,
  topups: (page: number, size: number) =>
    ['wallet', 'topups', page, size] as const,
  transactions: (page: number, size: number) =>
    ['wallet', 'transactions', page, size] as const,
  order: (orderNo: string) => ['wallet', 'order', orderNo] as const,
}

function numOrNull(v: unknown): number | null {
  return typeof v === 'number' && Number.isFinite(v) ? v : null
}

function strOrEmpty(v: unknown): string {
  return typeof v === 'string' ? v : ''
}

export function normalizeWalletInfo(raw: unknown): WalletInfo {
  const r = (raw ?? {}) as Record<string, unknown>
  return {
    source: r.source === 'internal' ? 'internal' : 'platform',
    balance: numOrNull(r.balance),
    frozen: numOrNull(r.frozen),
    available: numOrNull(r.available),
    lifetime_topup: numOrNull(r.lifetime_topup),
    lifetime_spend: numOrNull(r.lifetime_spend),
    pending_orders: numOrNull(r.pending_orders),
    topup_url: strOrEmpty(r.topup_url) || undefined,
  }
}

/** v1 handler: a platform outage is HTTP 200 {success:false, message}. */
export async function fetchWalletInfo(): Promise<WalletInfo> {
  return normalizeWalletInfo(await api.get<unknown>('/api/wallet/info'))
}

/**
 * The platform billing routes sit behind OIDC auth. A cookie session that is
 * not linked to a platform account answers 401/503 there; that must not send
 * the whole console to the sign-in page, hence skipAuthRedirect.
 */
export async function fetchPaymentMethods(): Promise<PaymentMethod[]> {
  const raw = await api.get<unknown>('/api/v2/user/billing/payment-methods', {
    skipAuthRedirect: true,
  })
  if (!Array.isArray(raw)) return []
  return raw
    .map((entry) => {
      const m = (entry ?? {}) as Record<string, unknown>
      return {
        id: strOrEmpty(m.id),
        name: strOrEmpty(m.name) || strOrEmpty(m.id),
        provider: strOrEmpty(m.provider),
        type: strOrEmpty(m.type),
      }
    })
    .filter((m) => m.id !== '')
}

export async function createCheckout(
  amountCny: number,
  paymentMethod: string,
  idempotencyKey: string
): Promise<CheckoutOrder> {
  const data = await api.post<Record<string, unknown>>(
    '/api/v2/user/billing/checkout',
    {
      amount_cny: amountCny,
      payment_method: paymentMethod,
      return_url: window.location.href,
    },
    {
      skipAuthRedirect: true,
      headers: { 'Idempotency-Key': idempotencyKey },
    }
  )
  return {
    order_no: strOrEmpty(data?.order_no),
    pay_url: strOrEmpty(data?.pay_url),
    status: strOrEmpty(data?.status),
    expires_at: strOrEmpty(data?.expires_at) || null,
  }
}

export async function fetchCheckoutStatus(
  orderNo: string
): Promise<CheckoutStatus> {
  const d = await api.get<Record<string, unknown>>(
    `/api/v2/user/billing/checkout/${encodeURIComponent(orderNo)}/status`,
    { skipAuthRedirect: true }
  )
  return {
    order_no: strOrEmpty(d?.order_no) || orderNo,
    status: strOrEmpty(d?.status),
    amount_cny: numOrNull(d?.amount_cny),
    pay_url: strOrEmpty(d?.pay_url),
    paid_at: strOrEmpty(d?.paid_at) || null,
    expires_at: strOrEmpty(d?.expires_at) || null,
  }
}

function normalizePage<T>(
  raw: unknown,
  row: (entry: Record<string, unknown>) => T
): PagedList<T> {
  const r = (raw ?? {}) as Record<string, unknown>
  const items = Array.isArray(r.items) ? r.items : []
  return {
    items: items.map((entry) => row((entry ?? {}) as Record<string, unknown>)),
    total: numOrNull(r.total) ?? 0,
    page: numOrNull(r.page) ?? 1,
    page_size: numOrNull(r.page_size) ?? items.length,
  }
}

export async function fetchTopups(
  page: number,
  size: number
): Promise<PagedList<TopupRecord>> {
  const raw = await tenantApi.get<unknown>(
    `/billing/topups?p=${page}&size=${size}`
  )
  return normalizePage(raw, (e) => ({
    id: numOrNull(e.id) ?? 0,
    content: strOrEmpty(e.content),
    quota: numOrNull(e.quota) ?? 0,
    created_at: numOrNull(e.created_at) ?? 0,
  }))
}

/** 503 for sessions that are not linked to a platform wallet. */
export async function fetchWalletTransactions(
  page: number,
  size: number
): Promise<PagedList<WalletTransaction>> {
  const raw = await api.get<unknown>(
    `/api/wallet/transactions?p=${page}&page_size=${size}`
  )
  return normalizePage(raw, (e) => ({
    id: numOrNull(e.id) ?? 0,
    type: strOrEmpty(e.type),
    amount: numOrNull(e.amount),
    balance_after: numOrNull(e.balance_after),
    description: strOrEmpty(e.description),
    reference_id: strOrEmpty(e.reference_id),
    created_at: strOrEmpty(e.created_at),
  }))
}
