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
import {
  render,
  screen,
  waitFor,
  fireEvent,
  cleanup,
} from '@testing-library/react';

import CreditPoolDrawer, {
  formatBalanceLine,
  isUnlimited,
  poolHealth,
  isTopupDisabled,
  validateMaxBalance,
  validateTopupAmount,
  UNLIMITED_SENTINEL,
} from './CreditPoolDrawer';

// Mock the helpers module: API.get / API.post + toasts.
vi.mock('../../../helpers', () => {
  const API = {
    get: vi.fn(),
    post: vi.fn(),
  };
  return {
    API,
    showError: vi.fn(),
    showSuccess: vi.fn(),
  };
});

import { API, showError, showSuccess } from '../../../helpers';

const FINITE_POOL = {
  id: 1,
  tenant_id: 't-acme',
  current_balance: 800,
  max_balance: 1000,
  reset_period: 'monthly',
};

const okGet = (data) => Promise.resolve({ data: { success: true, data } });
const usage = (draws) =>
  Promise.resolve({ data: { success: true, data: { draws } } });
const httpErr = (status, body) =>
  Promise.reject({ response: { status, data: body } });

beforeEach(() => {
  API.get.mockReset();
  API.post.mockReset();
  showError.mockReset();
  showSuccess.mockReset();
});

// ─── Pure helpers (no DOM) ──────────────────────────────────────────────────

describe('CreditPoolDrawer pure helpers', () => {
  it('isUnlimited recognises -1 sentinel', () => {
    expect(isUnlimited(null)).toBe(false);
    expect(isUnlimited({ max_balance: 0 })).toBe(false);
    expect(isUnlimited({ max_balance: UNLIMITED_SENTINEL })).toBe(true);
    expect(isUnlimited({ max_balance: 1000 })).toBe(false);
  });

  it('formatBalanceLine covers no-pool / unlimited / finite', () => {
    expect(formatBalanceLine(null)).toMatch(/no credit pool/i);
    expect(formatBalanceLine({ current_balance: 50, max_balance: -1 })).toMatch(
      /∞/,
    );
    expect(formatBalanceLine({ current_balance: 800, max_balance: 1000 })).toBe(
      '800 / 1000',
    );
  });

  it('poolHealth classifies tenant state', () => {
    expect(poolHealth(null)).toBe('unconfigured');
    expect(poolHealth({ max_balance: -1, current_balance: 99 })).toBe(
      'unlimited',
    );
    expect(poolHealth({ max_balance: 1000, current_balance: 0 })).toBe(
      'exhausted',
    );
    expect(poolHealth({ max_balance: 1000, current_balance: 100 })).toBe(
      'warning',
    ); // 10% < 20%
    expect(poolHealth({ max_balance: 1000, current_balance: 800 })).toBe('ok');
  });

  it('isTopupDisabled when no pool', () => {
    expect(isTopupDisabled(null)).toBe(true);
    expect(isTopupDisabled(FINITE_POOL)).toBe(false);
  });

  it('validateMaxBalance accepts -1 / 0 / positive; rejects -5 / NaN', () => {
    expect(validateMaxBalance('-1')).toBeNull();
    expect(validateMaxBalance('0')).toBeNull();
    expect(validateMaxBalance('1000000')).toBeNull();
    expect(validateMaxBalance('-5')).toMatch(/>= 0 or -1/);
    expect(validateMaxBalance('hello')).toMatch(/must be a number/);
  });

  it('validateTopupAmount requires positive integer', () => {
    expect(validateTopupAmount('100')).toBeNull();
    expect(validateTopupAmount('0')).toMatch(/> 0/);
    expect(validateTopupAmount('-5')).toMatch(/> 0/);
    expect(validateTopupAmount('1.5')).toMatch(/integer/);
  });
});

// ─── Drawer render flows ────────────────────────────────────────────────────

describe('CreditPoolDrawer render flows', () => {
  it('renders empty state when GET returns 404', async () => {
    API.get
      .mockImplementationOnce(() =>
        httpErr(404, { success: false, message: 'not found' }),
      )
      .mockImplementationOnce(() => usage([]));

    render(<CreditPoolDrawer tenantId='t-empty' onClose={() => {}} />);

    await waitFor(() =>
      expect(screen.getByTestId('ceiling-form')).toBeInTheDocument(),
    );
    expect(screen.getByText(/no credit pool/i)).toBeInTheDocument();
    expect(screen.getByTestId('topup-submit')).toBeDisabled();
  });

  it('renders pool + topup form when GET returns finite pool', async () => {
    API.get
      .mockImplementationOnce(() => okGet(FINITE_POOL))
      .mockImplementationOnce(() =>
        usage([
          {
            id: 9,
            created_at: Date.now(),
            direction: 1,
            amount: 50,
            reason: 'relay_debit',
          },
        ]),
      );

    render(<CreditPoolDrawer tenantId='t-acme' onClose={() => {}} />);

    await waitFor(() =>
      expect(screen.getByTestId('topup-submit')).not.toBeDisabled(),
    );
    expect(screen.getByTestId('pool-health')).toHaveTextContent('ok');
    expect(screen.getByTestId('pool-summary')).toHaveTextContent('800 / 1000');
    expect(screen.queryByTestId('ceiling-form')).not.toBeInTheDocument();
  });

  it('topup 402 shows wallet-insufficient notification', async () => {
    API.get
      .mockImplementationOnce(() => okGet(FINITE_POOL))
      .mockImplementationOnce(() => usage([]));
    API.post.mockImplementationOnce(() =>
      httpErr(402, { success: false, error_code: 'WALLET_DEBIT_FAILED' }),
    );

    render(<CreditPoolDrawer tenantId='t-acme' onClose={() => {}} />);
    await waitFor(() =>
      expect(screen.getByTestId('topup-submit')).not.toBeDisabled(),
    );

    fireEvent.change(screen.getByTestId('topup-amount'), {
      target: { value: '500' },
    });
    fireEvent.submit(screen.getByTestId('topup-form'));

    await waitFor(() => expect(showError).toHaveBeenCalled());
    expect(showError.mock.calls[0][0]).toMatch(/wallet debit failed/i);
  });

  it('topup 409 shows ceiling-exceed notification', async () => {
    API.get
      .mockImplementationOnce(() => okGet(FINITE_POOL))
      .mockImplementationOnce(() => usage([]));
    API.post.mockImplementationOnce(() =>
      httpErr(409, { success: false, error_code: 'POOL_CEILING_EXCEEDED' }),
    );

    render(<CreditPoolDrawer tenantId='t-acme' onClose={() => {}} />);
    await waitFor(() =>
      expect(screen.getByTestId('topup-submit')).not.toBeDisabled(),
    );

    fireEvent.change(screen.getByTestId('topup-amount'), {
      target: { value: '500' },
    });
    fireEvent.submit(screen.getByTestId('topup-form'));

    await waitFor(() => expect(showError).toHaveBeenCalled());
    expect(showError.mock.calls[0][0]).toMatch(/exceed pool ceiling/i);
  });

  it('topup form blocked on invalid amount (no API call made)', async () => {
    API.get
      .mockImplementationOnce(() => okGet(FINITE_POOL))
      .mockImplementationOnce(() => usage([]));

    render(<CreditPoolDrawer tenantId='t-acme' onClose={() => {}} />);
    await waitFor(() =>
      expect(screen.getByTestId('topup-submit')).not.toBeDisabled(),
    );

    fireEvent.change(screen.getByTestId('topup-amount'), {
      target: { value: '0' },
    });
    fireEvent.submit(screen.getByTestId('topup-form'));

    await waitFor(() => expect(showError).toHaveBeenCalled());
    expect(API.post).not.toHaveBeenCalled();
  });
});

// ─── Dialog semantics (HfDialog) ────────────────────────────────────────────

describe('CreditPoolDrawer as a dialog', () => {
  const open = async () => {
    API.get
      .mockImplementationOnce(() => okGet(FINITE_POOL))
      .mockImplementationOnce(() => usage([]));
    const onClose = vi.fn();
    render(
      <CreditPoolDrawer
        tenantId='t-acme'
        tenantName='Acme'
        onClose={onClose}
      />,
    );
    await waitFor(() =>
      expect(screen.getByTestId('topup-submit')).not.toBeDisabled(),
    );
    return onClose;
  };

  it('the panel is a modal dialog named after the tenant', async () => {
    await open();
    const dialog = screen.getByRole('dialog');
    expect(dialog).toHaveAttribute('aria-modal', 'true');
    expect(dialog).toHaveAccessibleName('Acme · credit pool');
    // The backdrop keeps its id for the page-level tests; the panel is the
    // dialog, not the backdrop.
    expect(screen.getByTestId('credit-pool-overlay')).not.toBe(dialog);
  });

  it('Escape inside the panel calls onClose once', async () => {
    const onClose = await open();
    fireEvent.keyDown(screen.getByRole('dialog'), { key: 'Escape' });
    expect(onClose).toHaveBeenCalledTimes(1);
  });

  it('a topup in flight refuses Escape and the X', async () => {
    let settle;
    API.post.mockImplementationOnce(
      () => new Promise((resolve) => (settle = resolve)),
    );
    const onClose = await open();
    fireEvent.change(screen.getByTestId('topup-amount'), {
      target: { value: '500' },
    });
    fireEvent.submit(screen.getByTestId('topup-form'));
    await waitFor(() =>
      expect(screen.getByRole('button', { name: 'close' })).toBeDisabled(),
    );
    fireEvent.keyDown(screen.getByRole('dialog'), { key: 'Escape' });
    expect(onClose).not.toHaveBeenCalled();
    settle({ data: { success: true, data: FINITE_POOL } });
    await waitFor(() =>
      expect(screen.getByRole('button', { name: 'close' })).not.toBeDisabled(),
    );
  });
});
