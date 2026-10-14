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
/** GET /api/wallet/info (handler.GetWalletInfo). Amounts are CNY. */
export interface WalletInfo {
  /** `platform`: the linked platform wallet. `internal`: quota shown as CNY. */
  source: 'platform' | 'internal'
  balance: number | null
  frozen: number | null
  available: number | null
  lifetime_topup: number | null
  lifetime_spend: number | null
  pending_orders: number | null
  /** Platform page for topping up, when the account is linked. */
  topup_url?: string
}

/** GET /api/v2/user/billing/payment-methods. */
export interface PaymentMethod {
  id: string
  name: string
  provider: string
  /** `qr` shows a code to scan; anything else is a redirect to a pay page. */
  type: string
}

/** POST /api/v2/user/billing/checkout (201). */
export interface CheckoutOrder {
  order_no: string
  pay_url: string
  status: string
  expires_at: string | null
}

/** GET /api/v2/user/billing/checkout/:order_no/status. */
export interface CheckoutStatus {
  order_no: string
  status: string
  amount_cny: number | null
  pay_url: string
  paid_at: string | null
  expires_at: string | null
}

/** One row of GET /api/v2/~/billing/topups (redemption credits). */
export interface TopupRecord {
  id: number
  content: string
  /** Internal quota units credited. */
  quota: number
  /** Unix seconds. */
  created_at: number
}

/** One row of GET /api/wallet/transactions (platform wallet ledger). */
export interface WalletTransaction {
  id: number
  type: string
  /** CNY; the platform's own sign convention. */
  amount: number | null
  balance_after: number | null
  description: string
  reference_id: string
  created_at: string
}

export interface PagedList<T> {
  items: T[]
  total: number
  page: number
  page_size: number
}
