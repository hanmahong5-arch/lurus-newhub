import { describe, expect, it } from 'vitest'

import { aggregateUsage } from '@/features/dashboard/lib/series'

import { rankModels } from './explore'

describe('rankModels', () => {
  it('orders by the chosen metric and reports share', () => {
    const usage = aggregateUsage(
      [
        { model_name: 'a', created_at: 100, quota: 10, count: 1, token_used: 5 },
        { model_name: 'b', created_at: 100, quota: 30, count: 2, token_used: 50 },
        { model_name: 'a', created_at: 200, quota: 10, count: 1, token_used: 5 },
      ],
      'hour',
      0,
      300
    )
    const byQuota = rankModels(usage, 'quota', 10)
    expect(byQuota[0]?.model).toBe('b')
    expect(byQuota[0]?.sharePct).toBeCloseTo(60, 5)
    expect(byQuota[1]?.model).toBe('a')

    const byTokens = rankModels(usage, 'tokens', 10)
    expect(byTokens[0]?.model).toBe('b')
    expect(byTokens[0]?.tokens).toBe(50)
  })
})
