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

import LimitsModal from './LimitsModal';

vi.mock('../../../helpers', () => ({
  API: { put: vi.fn() },
  showSuccess: vi.fn(),
}));

import { API, showSuccess } from '../../../helpers';

const TENANT = {
  id: 't-1',
  name: 'Acme',
  rate_limit_rpm: 60,
  rate_limit_tpm: 0,
};

beforeEach(() => {
  API.put.mockReset();
  showSuccess.mockReset();
});

describe('LimitsModal', () => {
  it('is a modal dialog named after the tenant, prefilled from the row', () => {
    render(
      <LimitsModal tenant={TENANT} onSaved={() => {}} onClose={() => {}} />,
    );
    const dialog = screen.getByRole('dialog');
    expect(dialog).toHaveAttribute('aria-modal', 'true');
    expect(dialog).toHaveAccessibleName('Acme · rate limits');
    const [rpm, tpm] = screen.getAllByRole('spinbutton');
    expect(rpm).toHaveValue(60);
    // 0 means unlimited and is shown as an empty field, not a literal 0.
    expect(tpm).toHaveValue(null);
  });

  it('Escape calls onClose once', () => {
    const onClose = vi.fn();
    render(
      <LimitsModal tenant={TENANT} onSaved={() => {}} onClose={onClose} />,
    );
    fireEvent.keyDown(screen.getByRole('dialog'), { key: 'Escape' });
    expect(onClose).toHaveBeenCalledTimes(1);
  });

  it('submit PUTs both caps as integers, 0 for blank, then calls onSaved', async () => {
    API.put.mockResolvedValue({ data: { success: true } });
    const onSaved = vi.fn();
    render(
      <LimitsModal tenant={TENANT} onSaved={onSaved} onClose={() => {}} />,
    );
    const [rpm, tpm] = screen.getAllByRole('spinbutton');
    fireEvent.change(rpm, { target: { value: '' } });
    fireEvent.change(tpm, { target: { value: '5000' } });
    fireEvent.click(screen.getByRole('button', { name: 'save' }));
    await waitFor(() => expect(onSaved).toHaveBeenCalledTimes(1));
    expect(API.put).toHaveBeenCalledWith('/api/v2/admin/tenants/t-1', {
      rate_limit_rpm: 0,
      rate_limit_tpm: 5000,
    });
    expect(showSuccess).toHaveBeenCalled();
  });

  it('refuses Escape while the PUT is in flight', async () => {
    let settle;
    API.put.mockImplementationOnce(
      () => new Promise((resolve) => (settle = resolve)),
    );
    const onClose = vi.fn();
    render(
      <LimitsModal tenant={TENANT} onSaved={() => {}} onClose={onClose} />,
    );
    fireEvent.click(screen.getByRole('button', { name: 'save' }));
    await waitFor(() =>
      expect(screen.getByRole('button', { name: 'close' })).toBeDisabled(),
    );
    fireEvent.keyDown(screen.getByRole('dialog'), { key: 'Escape' });
    expect(onClose).not.toHaveBeenCalled();
    settle({ data: { success: false } });
    await waitFor(() =>
      expect(screen.getByRole('button', { name: 'close' })).not.toBeDisabled(),
    );
  });
});
