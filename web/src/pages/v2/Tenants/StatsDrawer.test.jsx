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
import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, waitFor, fireEvent } from '@testing-library/react';

import StatsDrawer, { statRows } from './StatsDrawer';
import { getQuotaPerUSD } from '../../../helpers/formatting';

vi.mock('../../../helpers', () => ({ API: { get: vi.fn() } }));

import { API } from '../../../helpers';

const tr = (_k, fallback) => fallback;
const TENANT = { id: 't-1', name: 'Acme' };
const STATS = {
  tenant_id: 't-1',
  user_count: 3,
  max_users: 100,
  max_quota: 0,
  token_count: 12,
  channel_count: 2,
  total_quota_used: 0,
  total_quota_remaining: 0,
  active_subscriptions: 0,
  total_topup_amount: 0,
  total_redemptions: 1,
  log_count: 4200,
  last_activity_at: 0,
};

describe('statRows', () => {
  it('labels every row in words and formats the values', () => {
    const q = getQuotaPerUSD();
    const rows = Object.fromEntries(
      statRows({ ...STATS, total_quota_used: 5 * q, max_quota: 20 * q }, tr),
    );
    expect(rows['Seats used']).toBe('3 / 100');
    expect(rows['Quota used']).toBe('$5.00');
    expect(rows['Quota cap']).toBe('$20.00');
    expect(rows['Last activity']).toBe('Never');
  });

  it('says Unlimited for a zero cap and omits the fields the backend never fills', () => {
    const rows = statRows(STATS, tr);
    const labels = rows.map(([l]) => l);
    expect(Object.fromEntries(rows)['Quota cap']).toBe('Unlimited');
    for (const raw of Object.keys(STATS)) {
      expect(labels).not.toContain(raw);
    }
    expect(labels.join(' ')).not.toMatch(/subscription|top-?up/i);
  });
});

describe('StatsDrawer', () => {
  beforeEach(() => vi.clearAllMocks());

  it('renders human labels, not the wire field names', async () => {
    API.get.mockResolvedValue({ data: { success: true, data: STATS } });
    render(<StatsDrawer tenant={TENANT} onClose={() => {}} />);
    await screen.findByText('Seats used');
    expect(screen.getByRole('dialog')).toHaveAccessibleName('Acme · stats');
    expect(screen.queryByText('user_count')).toBeNull();
    expect(screen.queryByText('tenant_id')).toBeNull();
  });

  it('a failed read shows the shared error panel and retry refetches', async () => {
    API.get
      .mockRejectedValueOnce({ response: { status: 500, data: {} } })
      .mockResolvedValueOnce({ data: { success: true, data: STATS } });
    render(<StatsDrawer tenant={TENANT} onClose={() => {}} />);
    await screen.findByTestId('tenant-stats-error');
    fireEvent.click(screen.getByRole('button', { name: /retry/i }));
    await screen.findByText('Seats used');
    expect(API.get).toHaveBeenCalledTimes(2);
  });

  it('Escape closes the dialog', () => {
    API.get.mockReturnValue(new Promise(() => {}));
    const onClose = vi.fn();
    render(<StatsDrawer tenant={TENANT} onClose={onClose} />);
    fireEvent.keyDown(window, { key: 'Escape' });
    expect(onClose).toHaveBeenCalledTimes(1);
  });
});
