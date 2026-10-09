/**
 * The independent oracle for every money assertion: vendor list prices as the
 * vendor publishes them, and the two conversions newhub promises, written out
 * here by hand and never read from the instance or its source.
 *
 * Why by hand: the two worst billing defects of 2026-09 both passed every test
 * that computed the expected amount with the code under test. #212 billed
 * DeepSeek output at the input price; #210 took every $1 of usage from the
 * wallet as CNY 1. A test that asks newhub what something should cost agrees
 * with newhub by construction.
 *
 * When a vendor changes its price, change it here first and watch the
 * scenarios fail until the instance agrees.
 */

export interface ListPrice {
  /** USD per 1M input tokens. */
  input: number;
  /** USD per 1M output tokens. */
  output: number;
  source: string;
}

export const LIST_PRICES: Record<string, ListPrice> = {
  // deepseek-chat is a legacy name the vendor now answers as deepseek-flash.
  'deepseek-chat': {
    input: 0.3,
    output: 1.2,
    source:
      'api-docs.deepseek.com/quick_start/pricing, read 2026-09-23 (peak list price; #212)',
  },
  // Hosted TypeSafe (System One): input only, output free.
  'jev-latest': {
    input: 0.042,
    output: 0,
    source: 'TypeSafe hosted list price, cycle 21 (2026-10-01)',
  },
};

/** Quota units per US dollar: newhub's QuotaPerUnit. */
export const QUOTA_PER_USD = 500_000;

/** The platform wallet is in yuan; newhub converts at this rate. */
export const CNY_PER_USD = 7.3;

export interface Usage {
  prompt: number;
  completion: number;
}

/** The fake vendor's default report for every answer (fakeupstream.DefaultUsage). */
export const FAKE_USAGE: Usage = { prompt: 1000, completion: 500 };

/** Quota one call costs at list price, rounded to whole units. */
export function quotaFor(model: string, u: Usage = FAKE_USAGE): number {
  const p = LIST_PRICES[model];
  if (!p) throw new Error(`no list price for ${model} in the pricebook`);
  const usd = (u.prompt * p.input + u.completion * p.output) / 1e6;
  return Math.round(usd * QUOTA_PER_USD);
}

/**
 * What the platform books for a charge of `quota`: yuan at 4 decimals, the
 * precision of its decimal(14,4) wallet columns.
 */
export function walletCNYFor(quota: number): number {
  return Math.round((quota / QUOTA_PER_USD) * CNY_PER_USD * 1e4) / 1e4;
}

/** The same charge in the 0.0001-yuan units newhub records as logs.charged_cny4. */
export function cny4For(quota: number): number {
  return Math.round((quota / QUOTA_PER_USD) * CNY_PER_USD * 1e4);
}

/** Quota a platform top-up of `cny` yuan must credit locally. */
export function quotaForTopupCNY(cny: number): number {
  return Math.floor((cny / CNY_PER_USD) * QUOTA_PER_USD + 1e-9);
}
