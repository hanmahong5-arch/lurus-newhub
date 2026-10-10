/**
 * Quota -> money display, the single source for the new console.
 *
 * Internal quota units convert to USD by the server's `quota_per_unit`, then
 * to the operator's display currency. Nothing here carries a default for the
 * unit price: a deployment can override it, and a guessed figure shown as an
 * amount is wrong silently (the legacy v2 pages hard-coded 500000 and showed
 * the same balance as two different numbers). When /api/status has not
 * supplied the figure, `parseMoneyConfig` returns null and every formatter
 * renders the placeholder instead of a number.
 */

export type QuotaDisplayType = 'USD' | 'CNY' | 'TOKENS' | 'CUSTOM'

export interface MoneyConfig {
  /** Internal quota units per 1 USD (`quota_per_unit`). */
  quotaPerUnit: number
  displayType: QuotaDisplayType
  /** Currency symbol shown before the amount. */
  symbol: string
  /** Multiplier from USD to the display currency. */
  rate: number
}

/** Shown wherever an amount cannot be computed. */
export const MONEY_UNAVAILABLE = '--'

function positiveNumber(value: unknown): number | null {
  const n = typeof value === 'string' ? Number.parseFloat(value) : value
  return typeof n === 'number' && Number.isFinite(n) && n > 0 ? n : null
}

/**
 * Build the display config from a /api/status payload. Mirrors the legacy
 * console's getCurrencyConfig (web/src/helpers/render.jsx): USD (and the
 * LUTE variant, which that console also draws with "$") -> "$" x1,
 * CNY -> "¥" x usd_exchange_rate, CUSTOM -> custom symbol x custom rate,
 * TOKENS -> raw quota. Returns null when the unit price is missing/invalid,
 * or when the chosen currency lacks its rate.
 */
export function parseMoneyConfig(status: unknown): MoneyConfig | null {
  if (typeof status !== 'object' || status === null) return null
  const s = status as Record<string, unknown>
  const quotaPerUnit = positiveNumber(s.quota_per_unit)
  if (quotaPerUnit === null) return null

  const type = String(s.quota_display_type ?? 'USD').toUpperCase()
  if (type === 'TOKENS') {
    return { quotaPerUnit, displayType: 'TOKENS', symbol: '', rate: 1 }
  }
  if (type === 'CNY') {
    const rate = positiveNumber(s.usd_exchange_rate)
    if (rate === null) return null
    return { quotaPerUnit, displayType: 'CNY', symbol: '¥', rate }
  }
  if (type === 'CUSTOM') {
    const rate = positiveNumber(s.custom_currency_exchange_rate)
    if (rate === null) return null
    const symbol =
      typeof s.custom_currency_symbol === 'string' && s.custom_currency_symbol
        ? s.custom_currency_symbol
        : '¤'
    return { quotaPerUnit, displayType: 'CUSTOM', symbol, rate }
  }
  return { quotaPerUnit, displayType: 'USD', symbol: '$', rate: 1 }
}

/** Internal quota -> USD. */
export function quotaToUsd(quota: number, config: MoneyConfig): number {
  return quota / config.quotaPerUnit
}

/** USD -> internal quota (for inputs the person types in display money). */
export function usdToQuota(usd: number, config: MoneyConfig): number {
  return Math.round(usd * config.quotaPerUnit)
}

/**
 * Human-readable amount for a raw quota value. Without a config the
 * placeholder is returned. Amounts that round to zero but are not zero show
 * the smallest representable step rather than a misleading 0.
 */
export function formatQuota(
  quota: number | null | undefined,
  config: MoneyConfig | null,
  digits = 2
): string {
  if (config === null || typeof quota !== 'number' || !Number.isFinite(quota)) {
    return MONEY_UNAVAILABLE
  }
  if (config.displayType === 'TOKENS') {
    return Math.round(quota).toLocaleString('en-US')
  }
  const value = quotaToUsd(quota, config) * config.rate
  const fixed = value.toFixed(digits)
  if (Number.parseFloat(fixed) === 0) {
    if (value > 0) return config.symbol + (10 ** -digits).toFixed(digits)
    return config.symbol + (0).toFixed(digits)
  }
  return config.symbol + fixed
}
