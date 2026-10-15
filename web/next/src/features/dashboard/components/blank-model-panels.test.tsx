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

import { DistributionPanel, RecentCostPanel } from './panels'

const money: MoneyConfig = {
  quotaPerUnit: 1000,
  displayType: 'USD',
  symbol: '$',
  rate: 1,
}

describe('model rows with no model name', () => {
  // The nameless row holds the most spend of all.
  const rows = [
    { quota: 9000 },
    { model_name: 'a', quota: 1000 },
    { model_name: 'b', quota: 3000 },
  ]

  it('DistributionPanel labels the nameless row "Unknown model" and lists it last', () => {
    render(
      <DistributionPanel
        state='ok'
        money={money}
        data={{ rows, start: 0, end: 1 }}
      />
    )
    const list = screen.getAllByTestId('distribution-row')
    expect(list).toHaveLength(3)
    expect(list[0]).toHaveTextContent('b')
    expect(list[1]).toHaveTextContent('a')
    expect(list[2]).toHaveTextContent('Unknown model')
    expect(list[2]).toHaveTextContent('$9.00')
    // A readable label, not an empty cell and not a stand-in dash.
    expect(screen.queryByText('--')).toBeNull()
  })

  it('DistributionPanel sizes every bar against the largest row, not the first', () => {
    render(
      <DistributionPanel
        state='ok'
        money={money}
        data={{ rows, start: 0, end: 1 }}
      />
    )
    const widths = screen
      .getAllByTestId('distribution-row')
      .map((li) =>
        Number.parseFloat((li.querySelector('.bg-primary') as HTMLElement).style.width)
      )
    // b 3000, a 1000, nameless 9000 (the maximum).
    expect(widths[0]).toBeCloseTo(33.33, 1)
    expect(widths[1]).toBeCloseTo(11.11, 1)
    expect(widths[2]).toBe(100)
  })

  it('DistributionPanel shows a real model called "Unknown" as itself', () => {
    render(
      <DistributionPanel
        state='ok'
        money={money}
        data={{
          rows: [{ model_name: 'Unknown', quota: 1000 }, { quota: 2000 }],
          start: 0,
          end: 1,
        }}
      />
    )
    const list = screen.getAllByTestId('distribution-row')
    expect(list[0].textContent).toMatch(/^Unknown\$/)
    expect(list[1]).toHaveTextContent('Unknown model')
  })

  it('RecentCostPanel keeps nameless spend, as "Unknown model", last', () => {
    render(
      <RecentCostPanel
        state='ok'
        money={money}
        data={{
          total: 3,
          logs: [
            { type: 2, quota: 900 },
            { type: 2, quota: 100, model_name: 'a' },
            { type: 2, quota: 50, model_name: 'b' },
          ],
        }}
      />
    )
    const items = screen.getAllByRole('listitem')
    expect(items).toHaveLength(3)
    expect(items[0]).toHaveTextContent('a')
    expect(items[1]).toHaveTextContent('b')
    expect(items[2]).toHaveTextContent('Unknown model')
    expect(items[2]).toHaveTextContent('$0.9')
  })
})
