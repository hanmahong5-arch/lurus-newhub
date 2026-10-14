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
import { render, screen, waitFor } from '@testing-library/react'
import { describe, expect, it } from 'vitest'

import { getLobeIcon, getLobeIconNames } from './lobe-icon'
import { ProviderBadge } from './provider-badge'

describe('getLobeIcon', () => {
  it('draws the initial for an empty or unknown icon name', () => {
    const { container } = render(<div>{getLobeIcon('', 20)}</div>)
    expect(container).toHaveTextContent('?')
    render(<div data-testid='unknown'>{getLobeIcon('no-such-vendor', 20)}</div>)
    expect(screen.getByTestId('unknown')).toHaveTextContent('N')
  })

  it('lists the installed icon registry', () => {
    const names = getLobeIconNames()
    expect(names.length).toBeGreaterThan(50)
    expect(names).toEqual([...names].sort())
  })

  it('loads a registered icon as an svg', async () => {
    const [name] = getLobeIconNames()
    render(<div data-testid='known'>{getLobeIcon(name, 24)}</div>)
    await waitFor(() =>
      expect(screen.getByTestId('known').querySelector('svg')).not.toBeNull()
    )
  })
})

describe('ProviderBadge', () => {
  it('renders the label next to the fallback icon', () => {
    render(<ProviderBadge label='Vendor A' iconKey='no-such-vendor' />)
    expect(screen.getByText('Vendor A')).toBeInTheDocument()
  })
})
