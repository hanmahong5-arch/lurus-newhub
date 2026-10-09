import { describe, expect, it } from 'vitest'

import {
  DEFAULT_LOOKBACK_SEC,
  EMPTY_FILTERS,
  ID_FILTER_LOOKBACK_SEC,
  LOG_TYPE_ERROR,
  PAGE_SIZE,
  buildFilterParams,
  buildListParams,
  compactNumber,
  computeStartTimeSec,
  detailFields,
  isErrorLog,
  isSettlementFailed,
  latencyMs,
  normalizeList,
  pageCount,
  parseOther,
} from './logs'

const NOW = 1_800_000_000

describe('time window', () => {
  it('defaults to a bounded lookback anchored on now', () => {
    expect(computeStartTimeSec('', '', false, NOW)).toBe(
      NOW - DEFAULT_LOOKBACK_SEC
    )
  })

  it('an explicit start wins', () => {
    const start = '2026-01-02T03:04'
    expect(computeStartTimeSec(start, '', false, NOW)).toBe(
      Math.floor(new Date(start).getTime() / 1000)
    )
  })

  it('anchors on the end bound so start never lands after end', () => {
    const end = '2025-01-01T00:00'
    const endSec = Math.floor(new Date(end).getTime() / 1000)
    expect(computeStartTimeSec('', end, false, NOW)).toBe(
      endSec - DEFAULT_LOOKBACK_SEC
    )
  })

  it('an id search is windowed too', () => {
    expect(computeStartTimeSec('', '', true, NOW)).toBe(
      NOW - ID_FILTER_LOOKBACK_SEC
    )
  })
})

describe('query building', () => {
  it('sends the real field names the v2 handler binds', () => {
    const p = buildListParams(
      {
        ...EMPTY_FILTERS,
        model: ' model-x ',
        token: 'k1',
        errorsOnly: true,
        requestId: ' r-1 ',
        end: '2026-01-02T03:04',
      },
      3,
      NOW
    )
    expect(p.get('model_name')).toBe('model-x')
    expect(p.get('token_name')).toBe('k1')
    expect(p.get('type')).toBe(String(LOG_TYPE_ERROR))
    expect(p.get('request_id')).toBe('r-1')
    expect(p.get('page')).toBe('3')
    expect(p.get('page_size')).toBe(String(PAGE_SIZE))
    expect(p.get('end_time')).not.toBeNull()
    expect(p.has('start_time')).toBe(true)
  })

  it('omits empty filters; the stat query never carries request_id/page', () => {
    const p = buildFilterParams({ ...EMPTY_FILTERS, requestId: 'x' }, NOW)
    expect([...p.keys()]).toEqual(['start_time'])
  })
})

describe('other payload', () => {
  it('reads objects and JSON strings, treats corrupt as absent', () => {
    expect(parseOther({ other: { a: 1 } })).toEqual({ a: 1 })
    expect(parseOther({ other: '{"a":1}' })).toEqual({ a: 1 })
    expect(parseOther({ other: '{broken' })).toBeNull()
    expect(parseOther({ other: '[1]' })).toBeNull()
    expect(parseOther({})).toBeNull()
  })

  it('exposes only the detail fields the row carries', () => {
    expect(detailFields({})).toEqual({
      cacheReadTokens: null,
      cacheWriteTokens: null,
      endpoint: null,
      firstResponseMs: null,
      requestId: null,
      sessionId: null,
    })
    const f = detailFields({
      other: {
        cache_tokens: 7,
        cache_creation_tokens: 0,
        request_path: '/v1/chat/completions',
        frt: 120.4,
        request_id: 'r',
      },
    })
    expect(f.cacheReadTokens).toBe(7)
    expect(f.cacheWriteTokens).toBeNull()
    expect(f.endpoint).toBe('/v1/chat/completions')
    expect(f.firstResponseMs).toBe(120)
    expect(f.requestId).toBe('r')
  })

  it('flags a failed settlement', () => {
    expect(isSettlementFailed({ other: { settlement: 'failed' } })).toBe(true)
    expect(isSettlementFailed({ other: { settlement: 'ok' } })).toBe(false)
    expect(isSettlementFailed({})).toBe(false)
  })
})

describe('row helpers', () => {
  it('type 5 is an error row', () => {
    expect(isErrorLog({ type: 5 })).toBe(true)
    expect(isErrorLog({ type: 2 })).toBe(false)
  })

  it('latency is null when the relay recorded none', () => {
    expect(latencyMs({ total_latency_ms: 0 })).toBeNull()
    expect(latencyMs({ total_latency_ms: 85 })).toBe(85)
  })

  it('compacts large counts', () => {
    expect(compactNumber(999)).toBe('999')
    expect(compactNumber(1_250)).toBe('1.3K')
    expect(compactNumber(2_000_000)).toBe('2M')
    expect(compactNumber(undefined)).toBe('0')
  })

  it('a null logs array normalises to empty; pages never drop below 1', () => {
    expect(
      normalizeList({ logs: null, total: 0, page: 1, page_size: 20 })
    ).toEqual({ logs: [], total: 0 })
    expect(normalizeList(undefined)).toEqual({ logs: [], total: 0 })
    expect(pageCount(0)).toBe(1)
    expect(pageCount(PAGE_SIZE + 1)).toBe(2)
  })
})

describe('locale-following formatting', () => {
  it('formats times and counts by UI language, not the browser locale', async () => {
    const i18next = (await import('i18next')).default
    const { formatLogTime, compactNumber } = await import('./logs')
    const prev = i18next.language
    try {
      await i18next.changeLanguage('en')
      expect(formatLogTime(1_700_000_000)).toMatch(/PM|AM/)
      expect(compactNumber(1234)).toBe('1.2K')
      expect(compactNumber(999)).toBe('999')
      expect(compactNumber(0)).toBe('0')
      await i18next.changeLanguage('zh')
      const zh = formatLogTime(1_700_000_000)
      expect(zh).not.toMatch(/PM|AM/)
      expect(zh).toBe(
        new Date(1_700_000_000_000).toLocaleString('zh-CN')
      )
    } finally {
      await i18next.changeLanguage(prev)
    }
  })
})
