import { describe, expect, it } from 'vitest'

import { usageLogsSearchFromDrilldown } from './links'

describe('usageLogsSearchFromDrilldown', () => {
  it('builds start/end and omits blank model', () => {
    const s = usageLogsSearchFromDrilldown({
      model: '',
      startSec: 1_700_000_000,
      endSec: 1_700_086_400,
    })
    expect(s.model).toBeUndefined()
    expect(s.start).toMatch(/^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}$/)
    expect(s.end).toMatch(/^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}$/)
  })

  it('includes a named model for the Activity → logs href', () => {
    expect(
      usageLogsSearchFromDrilldown({
        model: 'claude-sonnet',
        startSec: 1_700_000_000,
        endSec: 1_700_003_600,
      }).model
    ).toBe('claude-sonnet')
  })
})
