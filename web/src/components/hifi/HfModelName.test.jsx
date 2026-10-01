/*
Copyright (C) 2025 QuantumNous

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
import React from 'react';
import { describe, it, expect } from 'vitest';
import { render, screen } from '@testing-library/react';

import { ChannelTypeLabel, ChannelTypeSelect } from './HfModelName';

describe('ChannelTypeLabel — System One channel types', () => {
  it.each([
    [57, 'TypeSafe', 'T'],
    [58, 'System One compatible (self-hosted)', 'S'],
  ])(
    'type %i renders its name and a lettered tile, not #id',
    (type, label, letter) => {
      render(<ChannelTypeLabel type={type} />);
      const row = screen.getByTestId('channel-type-label');
      expect(row.textContent).toBe(`${letter}${label}`);
      expect(screen.getByTestId('hf-vendor-icon').dataset.icon).toBe('');
    },
  );
});

describe('ChannelTypeSelect — System One channel types', () => {
  it('offers both types by numeric id', () => {
    render(<ChannelTypeSelect value={57} onChange={() => {}} />);
    const opts = [...screen.getByTestId('channel-type-select').options].map(
      (o) => [o.value, o.textContent],
    );
    expect(opts).toContainEqual(['57', 'TypeSafe · 57']);
    expect(opts).toContainEqual([
      '58',
      'System One compatible (self-hosted) · 58',
    ]);
  });
});
