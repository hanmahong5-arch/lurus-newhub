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
import { describe, expect, it } from 'vitest'

import type { ModelPerf } from '../types'
import {
  errorSeverity,
  fmtMs,
  fmtPct,
  nextSort,
  rangeSeconds,
  sortModels,
  summarize,
} from './derive'

function m(over: Partial<ModelPerf>): ModelPerf {
  return {
    model_name: 'model-a',
    requests: 0,
    errors: 0,
    error_rate: 0,
    prompt_tokens: 0,
    completion_tokens: 0,
    quota: 0,
    latency_samples: 0,
    avg_latency_ms: 0,
    p50_latency_ms: 0,
    p95_latency_ms: 0,
    ...over,
  }
}

describe('summarize', () => {
  it('weights latency by samples, not requests', () => {
    const s = summarize([
      m({ requests: 1000, latency_samples: 10, avg_latency_ms: 100 }),
      m({ requests: 10, latency_samples: 10, avg_latency_ms: 300 }),
    ])
    expect(s.weightedLatency).toBe(200)
    expect(s.totalSamples).toBe(20)
  })

  it('reports zero latency, not NaN, with no samples', () => {
    expect(summarize([m({ requests: 5 })]).weightedLatency).toBe(0)
    expect(summarize([]).totalRequests).toBe(0)
  })
})

describe('sorting', () => {
  it('sorts without mutating the input', () => {
    const input = [
      m({ model_name: 'a', requests: 1 }),
      m({ model_name: 'b', requests: 2 }),
    ]
    const out = sortModels(input, 'requests', 'desc')
    expect(out.map((x) => x.model_name)).toEqual(['b', 'a'])
    expect(input.map((x) => x.model_name)).toEqual(['a', 'b'])
  })

  it('nextSort flips the active column and resets others to descending', () => {
    expect(nextSort({ key: 'requests', dir: 'desc' }, 'requests')).toEqual({
      key: 'requests',
      dir: 'asc',
    })
    expect(nextSort({ key: 'requests', dir: 'asc' }, 'error_rate')).toEqual({
      key: 'error_rate',
      dir: 'desc',
    })
  })
})

describe('formatting', () => {
  it('dashes latency without samples and rounds otherwise', () => {
    expect(fmtMs(0, 0)).toBe('—')
    expect(fmtMs(12.6, 3)).toBe('13ms')
  })

  it('formats rates to two decimals', () => {
    expect(fmtPct(0.05)).toBe('5.00%')
    expect(fmtPct(undefined)).toBe('0.00%')
  })

  it('classifies error severity at the 1% and 5% thresholds', () => {
    expect(errorSeverity(0.01)).toBe('ok')
    expect(errorSeverity(0.011)).toBe('warn')
    expect(errorSeverity(0.05)).toBe('warn')
    expect(errorSeverity(0.051)).toBe('bad')
  })

  it('falls back to 24h for an unknown range', () => {
    expect(rangeSeconds('7d')).toBe(7 * 86400)
    expect(rangeSeconds('nope' as never)).toBe(86400)
  })
})
