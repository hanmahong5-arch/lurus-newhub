import { describe, expect, it } from 'vitest'

import { EMPTY_FILTERS } from './logs'
import { filtersFromSearch, searchFromFilters } from './url-search'

describe('usage-logs url search round-trip', () => {
  it('hydrates LogFilters from Activity drill-down search', () => {
    expect(
      filtersFromSearch({
        model: 'gpt-4o',
        start: '2026-10-01T00:00',
        end: '2026-10-02T00:00',
      })
    ).toMatchObject({
      model: 'gpt-4o',
      start: '2026-10-01T00:00',
      end: '2026-10-02T00:00',
      token: '',
      logType: 0,
    })
  })

  it('serializes applied filters sparsely for navigate()', () => {
    expect(
      searchFromFilters({
        ...EMPTY_FILTERS,
        model: 'claude',
        logType: 5,
        start: '2026-10-01T12:00',
      })
    ).toEqual({
      model: 'claude',
      start: '2026-10-01T12:00',
      type: 5,
    })
  })
})
