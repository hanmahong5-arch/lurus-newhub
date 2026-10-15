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
import { fireEvent, render, screen } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'

import { TierToggle } from './tier-toggle'

describe('TierToggle', () => {
  it('says "not tiered" for zero tiers and stays clickable: it is the add-a-tier entry point', () => {
    const onClick = vi.fn()
    render(
      <TierToggle count={0} expanded={false} onClick={onClick} testId='tier-cell-a' />
    )
    const btn = screen.getByTestId('tier-cell-a')
    expect(btn.textContent).toContain('Not tiered')
    expect(btn).not.toBeDisabled()
    expect(btn).toHaveAttribute('data-tiered', 'false')
    fireEvent.click(btn)
    expect(onClick).toHaveBeenCalledTimes(1)
  })

  it('shows the tier count, in a stronger weight, once tiers exist', () => {
    render(
      <TierToggle count={2} expanded={false} onClick={() => {}} testId='tier-cell-b' />
    )
    const btn = screen.getByTestId('tier-cell-b')
    expect(btn.textContent).toContain('2 tiers')
    expect(btn).toHaveAttribute('data-tiered', 'true')
    expect(btn.className).toContain('font-semibold')
  })

  it('uses the singular for one tier', () => {
    render(<TierToggle count={1} expanded onClick={() => {}} testId='tier-cell-c' />)
    const btn = screen.getByTestId('tier-cell-c')
    expect(btn.textContent).toContain('1 tier')
    expect(btn.textContent).not.toContain('1 tiers')
    expect(btn).toHaveAttribute('aria-expanded', 'true')
  })

  it('a read-only viewer sees "no tiers", with no add hint', () => {
    render(<TierToggle count={0} expanded={false} readOnly onClick={() => {}} testId='tier-cell-d' />)
    const btn = screen.getByTestId('tier-cell-d')
    expect(btn.textContent).toContain('No tiers')
    expect(btn.textContent).not.toContain('+')
  })
})
