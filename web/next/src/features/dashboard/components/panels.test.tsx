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
import { render, screen } from '@testing-library/react'
import { describe, expect, it } from 'vitest'

import type { MoneyConfig } from '@/lib/money'
import type { CurrentUser } from '@/lib/user'

import { formatRemaining, loadState } from '../lib/state'

import {
  DistributionPanel,
  KpiCards,
  LivePanel,
  RecentCostPanel,
  TrendPanel,
} from './panels'

const money: MoneyConfig = {
  quotaPerUnit: 1000,
  displayType: 'USD',
  symbol: '$',
  rate: 1,
}

const me: CurrentUser = {
  id: 1,
  username: 'u',
  role: 1,
  quota: 0,
  used_quota: 2500,
  remaining_quota: 7500,
  request_count: 1234,
  token_count: 3,
}

describe('loadState', () => {
  it('error wins over pending', () => {
    expect(loadState({ isPending: true, isError: true })).toBe('error')
    expect(loadState({ isPending: true, isError: false })).toBe('loading')
    expect(loadState({ isPending: false, isError: false })).toBe('ok')
  })
})

describe('formatRemaining', () => {
  it('converts by the server unit price; negative means unlimited', () => {
    expect(formatRemaining(7500, money)).toBe('$7.50')
    expect(formatRemaining(-1, money)).toBe('∞')
    expect(formatRemaining(undefined, money)).toBe('--')
    expect(formatRemaining(7500, null)).toBe('--')
  })
})

describe('KpiCards', () => {
  it('maps user/me fields using quota_per_unit, not a constant', () => {
    render(<KpiCards me={me} meState='ok' money={money} trend={undefined} />)
    expect(screen.getByTestId('kpi-balance')).toHaveTextContent('$7.50')
    expect(screen.getByTestId('kpi-spent')).toHaveTextContent('$2.50')
    expect(screen.getByTestId('kpi-requests')).toHaveTextContent('1,234')
    expect(screen.getByTestId('kpi-keys')).toHaveTextContent('3')
  })
  it('a failed read shows an error, not zeros', () => {
    render(
      <KpiCards me={undefined} meState='error' money={money} trend={undefined} />
    )
    expect(screen.getByTestId('kpi-balance')).toHaveTextContent(
      'Unable to load'
    )
    expect(screen.getByTestId('kpi-spent')).not.toHaveTextContent('$0')
  })
  it('without a money config amounts are the placeholder', () => {
    render(<KpiCards me={me} meState='ok' money={null} trend={undefined} />)
    expect(screen.getByTestId('kpi-spent')).toHaveTextContent('--')
  })
})

describe('LivePanel', () => {
  it('failed read -> alert, never the idle message', () => {
    render(<LivePanel state='error' data={undefined} />)
    expect(screen.getByTestId('dashboard-load-failed')).toBeInTheDocument()
    expect(screen.queryByTestId('dashboard-live-idle')).toBeNull()
  })
  it('settled with no rows -> idle message', () => {
    render(<LivePanel state='ok' data={{ logs: [], total: 0 }} />)
    expect(screen.getByTestId('dashboard-live-idle')).toBeInTheDocument()
  })
  it('computes qps from the server total and latency from rows', () => {
    render(
      <LivePanel
        state='ok'
        data={{ logs: [{ type: 2, total_latency_ms: 200 }], total: 600 }}
      />
    )
    expect(screen.getByTestId('dashboard-live')).toHaveTextContent('200ms')
    expect(screen.getByTestId('dashboard-live')).toHaveTextContent('Requests / sec2')
  })
})

describe('failed read is not an empty list', () => {
  it('TrendPanel', () => {
    render(<TrendPanel state='error' data={undefined} money={money} />)
    expect(screen.getByTestId('dashboard-load-failed')).toBeInTheDocument()
    expect(screen.queryByTestId('dashboard-trend-empty')).toBeNull()
  })
  it('TrendPanel is empty only when the read succeeded with no rows', () => {
    render(
      <TrendPanel
        state='ok'
        data={{ rows: [], start: 0, end: 1 }}
        money={money}
      />
    )
    expect(screen.getByTestId('dashboard-trend-empty')).toBeInTheDocument()
  })
  it('DistributionPanel', () => {
    const { unmount } = render(
      <DistributionPanel state='error' data={undefined} money={money} />
    )
    expect(screen.getByTestId('dashboard-load-failed')).toBeInTheDocument()
    expect(screen.queryByTestId('dashboard-distribution-empty')).toBeNull()
    unmount()
    render(
      <DistributionPanel
        state='ok'
        money={money}
        data={{
          rows: [
            { model_name: 'a', quota: 1000 },
            { model_name: 'b', quota: 3000 },
          ],
          start: 0,
          end: 1,
        }}
      />
    )
    const rows = screen.getAllByTestId('distribution-row')
    expect(rows[0]).toHaveTextContent('b')
    expect(rows[0]).toHaveTextContent('$3.00')
  })
  it('RecentCostPanel', () => {
    const { unmount } = render(
      <RecentCostPanel state='error' data={undefined} money={money} />
    )
    expect(screen.getByTestId('dashboard-load-failed')).toBeInTheDocument()
    expect(screen.queryByTestId('dashboard-recent-cost-empty')).toBeNull()
    unmount()
    render(
      <RecentCostPanel
        state='ok'
        data={{ logs: [], total: 0 }}
        money={money}
      />
    )
    expect(
      screen.getByTestId('dashboard-recent-cost-empty')
    ).toBeInTheDocument()
  })
})
