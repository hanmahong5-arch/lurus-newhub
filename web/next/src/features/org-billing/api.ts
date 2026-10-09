import { api, http, tenantApi, tenantUrl } from '@/lib/api'

import {
  normalizeInvoices,
  normalizeStatement,
  normalizeSummary,
  statementQuery,
  type GroupBy,
  type InvoiceMonth,
  type StatementData,
  type WalletSummary,
} from './lib/statement'

export async function fetchStatement(
  month: string,
  groupBy: GroupBy
): Promise<StatementData> {
  const raw = await tenantApi.get<unknown>(
    `/billing/statement?${statementQuery(month, groupBy)}`
  )
  return normalizeStatement(raw)
}

export async function fetchInvoices(): Promise<InvoiceMonth[]> {
  return normalizeInvoices(await tenantApi.get<unknown>('/billing/invoices'))
}

/** Platform wallet (same source as the legacy Billing page). */
export async function fetchWallet(): Promise<WalletSummary> {
  return normalizeSummary(
    await api.get<unknown>('/api/v2/user/billing/summary')
  )
}

export async function redeemCode(
  key: string
): Promise<{ quota_added: number }> {
  const data = await tenantApi.post<{ quota_added?: number }>('/redeem', {
    key,
  })
  return { quota_added: data?.quota_added ?? 0 }
}

/** Whole-tenant CSV export; failures are thrown with the server's message. */
export async function downloadStatementCsv(
  month: string,
  groupBy: GroupBy
): Promise<{ blob: Blob; filename: string }> {
  const res = await http.get<Blob>(
    `${tenantUrl('/billing/statement')}?${statementQuery(month, groupBy, 'csv')}`,
    { responseType: 'blob', validateStatus: () => true }
  )
  const type = String(res.headers['content-type'] ?? '')
  if (res.status !== 200 || !type.includes('text/csv')) {
    let message = ''
    try {
      const body = JSON.parse(await res.data.text()) as { message?: string }
      message = body.message ?? ''
    } catch {
      /* not JSON */
    }
    throw new Error(message || `Export failed (${res.status})`)
  }
  return { blob: res.data, filename: `statement-${month}-${groupBy}.csv` }
}
