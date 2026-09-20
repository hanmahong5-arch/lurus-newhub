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

// The real i18n instance (not a stub t), so the assertions below are on the
// copy a customer sees and a missing key would fail here as well as in the
// i18n reference gate.
import '../../../i18n/i18n';
import LoadErrorPanel, { KpiCaption, captionText } from './LoadErrorPanel';

describe('LoadErrorPanel', () => {
  it('renders the banner copy and a retry control', () => {
    render(<LoadErrorPanel onRetry={() => {}} />);
    const panel = screen.getByTestId('dashboard-load-error');
    expect(panel.textContent).toContain('Unable to load');
    expect(screen.getByTestId('dashboard-retry-btn')).toBeTruthy();
  });

  it('calls onRetry when the retry control is clicked', () => {
    const onRetry = vi.fn();
    render(<LoadErrorPanel onRetry={onRetry} />);
    fireEvent.click(screen.getByTestId('dashboard-retry-btn'));
    expect(onRetry).toHaveBeenCalledTimes(1);
  });
});

describe('captionText', () => {
  it('keeps the confirmed-empty copy when the fetch answered', () => {
    expect(
      captionText('ok', 'no traffic in last 5 min', 'unable to load'),
    ).toBe('no traffic in last 5 min');
  });

  it('says the pending copy — never the confirmed-empty copy — before the first attempt settles', () => {
    // null is the "not attempted yet" sentinel. The caption is on screen
    // under the "…" KPI number at that point, so "no traffic in last 5 min"
    // there would assert a check that has not run. Mutation that must turn
    // this red: drop the null branch (the first cut returned okText).
    expect(
      captionText(
        null,
        'no traffic in last 5 min',
        'unable to load',
        'loading…',
      ),
    ).toBe('loading…');
    expect(
      captionText(
        undefined,
        'no traffic in last 5 min',
        'unable to load',
        'loading…',
      ),
    ).toBe('loading…');
    // No pending copy given: an empty caption, which asserts nothing.
    expect(
      captionText(null, 'no traffic in last 5 min', 'unable to load'),
    ).toBe('');
  });

  it.skip('(superseded) kept the confirmed-empty copy before the first attempt settles', () => {
    // Retained as the record of the first cut's behaviour; the test above
    // is the live oracle.
    // a statement about the account.
    expect(
      captionText(null, 'no traffic in last 5 min', 'unable to load'),
    ).toBe('no traffic in last 5 min');
  });

  it.each(['error', 'forbidden', 'unauthenticated'])(
    'swaps in the could-not-check copy for a %s outcome',
    (status) => {
      expect(
        captionText(status, 'no traffic in last 5 min', 'unable to load'),
      ).toBe('unable to load');
    },
  );
});

describe('KpiCaption', () => {
  it('renders whatever caption text it is given', () => {
    render(<KpiCaption>awaiting requests with latency data</KpiCaption>);
    expect(
      screen.getByText('awaiting requests with latency data'),
    ).toBeTruthy();
  });
});
