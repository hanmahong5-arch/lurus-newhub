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
import { beforeEach, describe, expect, it, vi, type MockInstance } from 'vitest'

import { ApiError, http } from '@/lib/api'

import {
  createCheckout,
  fetchCheckoutStatus,
  fetchPaymentMethods,
  fetchTopups,
  fetchWalletInfo,
  fetchWalletTransactions,
} from './api'

// restoreMocks is on, so the spy is (re)created for every test.
let request: MockInstance<typeof http.request>

function reply(data: unknown, status = 200) {
  request.mockResolvedValueOnce({ data, status } as never)
}

beforeEach(() => {
  request = vi.spyOn(http, 'request')
})

describe('wallet api wire contract', () => {
  it('sends the idempotency key as a header, not in the body', async () => {
    reply(
      {
        success: true,
        data: { order_no: 'O1', pay_url: 'u', status: 'pending' },
      },
      201
    )
    const order = await createCheckout(12.5, 'pay-qr', 'key-123')
    const config = request.mock.calls[0][0]
    expect(config.method).toBe('POST')
    expect(config.url).toBe('/api/v2/user/billing/checkout')
    expect(config.headers).toMatchObject({ 'Idempotency-Key': 'key-123' })
    expect(config.data).toMatchObject({
      amount_cny: 12.5,
      payment_method: 'pay-qr',
    })
    expect(config.data).not.toHaveProperty('idempotency_key')
    // The OIDC-only billing routes must not bounce the console to sign-in.
    expect(config.skipAuthRedirect).toBe(true)
    expect(order).toMatchObject({
      order_no: 'O1',
      pay_url: 'u',
      expires_at: null,
    })
  })

  it('surfaces a business failure with the platform message', async () => {
    reply({ success: false, message: 'bad amount' }, 400)
    await expect(createCheckout(1, 'm', 'k')).rejects.toMatchObject({
      message: 'bad amount',
    })
  })

  it('reads the order status by an encoded order number', async () => {
    reply({
      success: true,
      data: { order_no: 'A/B', status: 'paid', amount_cny: 3 },
    })
    const status = await fetchCheckoutStatus('A/B')
    expect(request.mock.calls[0][0].url).toBe(
      '/api/v2/user/billing/checkout/A%2FB/status'
    )
    expect(status).toMatchObject({ status: 'paid', amount_cny: 3 })
  })

  it('keeps only payment methods that have an id', async () => {
    reply({
      success: true,
      data: [{ id: 'a', name: 'A', type: 'qr' }, { name: 'no id' }, null],
    })
    expect(await fetchPaymentMethods()).toEqual([
      { id: 'a', name: 'A', provider: '', type: 'qr' },
    ])
  })

  it('treats a non-list method payload as no methods', async () => {
    reply({ success: true, data: null })
    expect(await fetchPaymentMethods()).toEqual([])
  })

  it('maps wallet info and rejects a platform outage', async () => {
    reply({
      success: true,
      data: {
        source: 'platform',
        balance: 5,
        frozen: 1,
        available: 4,
        topup_url: 'x',
      },
    })
    expect(await fetchWalletInfo()).toMatchObject({
      source: 'platform',
      balance: 5,
      available: 4,
      topup_url: 'x',
      lifetime_topup: null,
    })
    reply({ success: false, message: 'platform billing unavailable' })
    await expect(fetchWalletInfo()).rejects.toBeInstanceOf(ApiError)
  })

  it('requests the history pages with the server parameter names', async () => {
    reply({
      success: true,
      data: { items: [{ id: 1, quota: 5 }], total: 1, page: 2, page_size: 20 },
    })
    const topups = await fetchTopups(2, 20)
    expect(request.mock.calls[0][0].url).toBe(
      '/api/v2/~/billing/topups?p=2&size=20'
    )
    expect(topups.items[0]).toMatchObject({ id: 1, quota: 5, content: '' })

    reply({ success: true, data: { items: null, total: 0 } })
    const tx = await fetchWalletTransactions(3, 10)
    expect(request.mock.calls[1][0].url).toBe(
      '/api/wallet/transactions?p=3&page_size=10'
    )
    expect(tx).toMatchObject({ items: [], total: 0 })
  })
})
