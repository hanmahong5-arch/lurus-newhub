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
import { describe, it, expect, vi } from 'vitest';
import { render, screen, fireEvent } from '@testing-library/react';

import TierCell from './TierCell';

// A small i18n stub that actually resolves the plural-count key — the
// shared mock other Pricing tests use does not (see Pricing/index.test.jsx),
// but this component's own text is exactly what this file checks.
const tr = (key, fallback) => {
  if (key === 'console.pricing.context_tiers_count') {
    const n = fallback?.count ?? 0;
    return `${n} tier${n === 1 ? '' : 's'}`;
  }
  if (typeof fallback === 'string') return fallback;
  return key;
};

describe('TierCell', () => {
  it('renders "not tiered" for zero tiers and stays clickable — it is the add-a-tier entry point', () => {
    const onClick = vi.fn();
    render(
      <TierCell count={0} onClick={onClick} tr={tr} testId='tier-cell-a' />,
    );
    const btn = screen.getByTestId('tier-cell-a');
    expect(btn.textContent).toContain('not tiered');
    expect(btn).not.toBeDisabled();
    fireEvent.click(btn);
    expect(onClick).toHaveBeenCalledTimes(1);
  });

  it('renders the tier count, in a stronger weight, once tiers exist', () => {
    render(
      <TierCell count={2} onClick={() => {}} tr={tr} testId='tier-cell-b' />,
    );
    const btn = screen.getByTestId('tier-cell-b');
    expect(btn.textContent).toContain('2 tiers');
    expect(btn.style.fontWeight).toBe('600');
  });
});
