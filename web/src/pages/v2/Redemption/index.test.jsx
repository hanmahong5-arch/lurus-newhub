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
import { fireEvent, render, screen, waitFor } from '@testing-library/react';

// ─── mocks ───────────────────────────────────────────────────────────────────

vi.mock('../../../helpers', () => ({
  API: {
    get: vi.fn(),
    post: vi.fn(),
    delete: vi.fn(),
  },
  showError: vi.fn(),
  showSuccess: vi.fn(),
}));

vi.mock('../../../components/hifi/HFShell', () => ({
  default: ({ children, actions }) =>
    React.createElement(
      'div',
      { 'data-testid': 'hf-shell' },
      React.createElement('div', { 'data-testid': 'hf-actions' }, actions),
      children,
    ),
}));

// ConfirmDialog mock — renders when visible=true with confirm/cancel buttons.
vi.mock('../../../components/common/ConfirmDialog', () => ({
  default: ({ visible, onConfirm, onCancel, title }) =>
    visible
      ? React.createElement(
          'div',
          { 'data-testid': 'confirm-dialog' },
          React.createElement(
            'span',
            { 'data-testid': 'confirm-dialog-title' },
            title,
          ),
          React.createElement(
            'button',
            { 'data-testid': 'confirm-ok', onClick: onConfirm },
            'ok',
          ),
          React.createElement(
            'button',
            { 'data-testid': 'confirm-cancel', onClick: onCancel },
            'cancel',
          ),
        )
      : null,
}));

// Mirror i18next's en behaviour: return the English defaultValue (2nd arg)
// with {{var}} interpolation, falling back to the key when no default given.
vi.mock('react-i18next', () => ({
  useTranslation: () => ({
    t: (key, fallback, opts) => {
      const vars =
        typeof fallback === 'object' && fallback !== null ? fallback : opts;
      let out = typeof fallback === 'string' ? fallback : key;
      if (vars) {
        for (const [k, v] of Object.entries(vars)) {
          out = out.split(`{{${k}}}`).join(String(v));
        }
      }
      return out;
    },
  }),
  // helpers/formatting (formatTime) imports the i18n instance, which
  // registers itself with react-i18next at module load.
  initReactI18next: { type: '3rdParty', init: () => {} },
}));

import HFRedemption from './index';
import { API, showError, showSuccess } from '../../../helpers';

// ─── fixtures ────────────────────────────────────────────────────────────────

const makeRedemption = (id, name, status = 1) => ({
  id,
  key: `abcd****************************${id}`.slice(0, 32),
  name,
  quota: 500000,
  status,
  created_time: 1716000000,
  expired_time: 0,
  used_user_id: status === 3 ? 7 : 0,
});

const mockListOk = (items) => ({
  data: {
    success: true,
    data: { redemptions: items, total: items.length, page: 1, page_size: 50 },
  },
});

beforeEach(() => {
  API.get.mockReset();
  API.post.mockReset();
  API.delete.mockReset();
  showError.mockReset();
  showSuccess.mockReset();
  window.localStorage.clear();
  window.localStorage.setItem('tenant_slug', 'acme');
});

// ─── tests ───────────────────────────────────────────────────────────────────

describe('Redemption page', () => {
  // 1. Renders the redemption list from API.
  it('renders redemption list', async () => {
    const items = [
      makeRedemption(1, 'promo-2025'),
      makeRedemption(2, 'partner-code', 3),
    ];
    API.get.mockResolvedValue(mockListOk(items));

    render(React.createElement(HFRedemption));

    await waitFor(() => {
      expect(screen.getByTestId('redemption-row-1')).toBeDefined();
      expect(screen.getByTestId('redemption-row-2')).toBeDefined();
    });

    // Verify API was called with correct tenant slug.
    expect(API.get).toHaveBeenCalledWith(
      expect.stringContaining('/api/v2/~/redemptions'),
      expect.anything(),
    );

    // Delete buttons use data-testid with row id.
    expect(screen.getByTestId('redemption-delete-btn-1')).toBeDefined();
    expect(screen.getByTestId('redemption-delete-btn-2')).toBeDefined();
  });

  // 2. Empty state shown when list is empty.
  it('shows empty state when no redemptions', async () => {
    API.get.mockResolvedValue(mockListOk([]));

    render(React.createElement(HFRedemption));

    await waitFor(() => {
      expect(screen.getByTestId('redemption-empty')).toBeDefined();
    });
  });

  // 3. Create flow — opens modal, submits, shows keys banner, refreshes list.
  it('create flow shows keys banner after success', async () => {
    // First fetch: empty list. Second fetch (after create): list with new item.
    API.get
      .mockResolvedValueOnce(mockListOk([]))
      .mockResolvedValueOnce(mockListOk([makeRedemption(10, 'batch-promo')]));

    API.post.mockResolvedValue({
      data: {
        success: true,
        data: {
          codes: [{ id: 10, key: 'a'.repeat(32), name: 'batch-promo' }],
          count: 1,
          requested: 1,
        },
      },
    });

    render(React.createElement(HFRedemption));

    // Wait for initial load.
    await waitFor(() => expect(API.get).toHaveBeenCalledTimes(1));

    // Open create modal.
    fireEvent.click(screen.getByTestId('redemption-create-btn'));

    // Fill name field.
    await waitFor(() => screen.getByTestId('redemption-name-input'));
    fireEvent.change(screen.getByTestId('redemption-name-input'), {
      target: { value: 'batch-promo' },
    });

    // Submit.
    fireEvent.click(screen.getByTestId('redemption-create-submit'));

    await waitFor(() => {
      expect(API.post).toHaveBeenCalledWith(
        `/api/v2/~/redemptions`,
        expect.objectContaining({ name: 'batch-promo' }),
      );
    });

    // Keys banner should appear showing the new codes.
    await waitFor(() => {
      expect(screen.getByTestId('redemption-keys-list')).toBeDefined();
    });

    expect(showSuccess).toHaveBeenCalledWith('Redemption codes generated');

    // List should be refreshed — row 10 visible.
    await waitFor(() => {
      expect(screen.getByTestId('redemption-row-10')).toBeDefined();
    });
  });

  // 4. Delete with confirm dialog — happy path.
  it('delete with confirm dialog calls DELETE and refreshes list', async () => {
    const item = makeRedemption(5, 'to-delete');
    API.get
      .mockResolvedValueOnce(mockListOk([item]))
      .mockResolvedValueOnce(mockListOk([]));
    API.delete.mockResolvedValue({ data: { success: true } });

    render(React.createElement(HFRedemption));

    await waitFor(() => screen.getByTestId('redemption-delete-btn-5'));

    // Click delete button — confirm dialog should open.
    fireEvent.click(screen.getByTestId('redemption-delete-btn-5'));

    await waitFor(() => {
      expect(screen.getByTestId('confirm-dialog')).toBeDefined();
    });

    // Confirm deletion.
    fireEvent.click(screen.getByTestId('confirm-ok'));

    await waitFor(() => {
      expect(API.delete).toHaveBeenCalledWith(`/api/v2/~/redemptions/5`);
    });

    expect(showSuccess).toHaveBeenCalledWith('Redemption code deleted');

    // After delete, list is refreshed and empty state shown.
    await waitFor(() => {
      expect(screen.getByTestId('redemption-empty')).toBeDefined();
    });
  });

  // 5. Delete cancel — does NOT call API.delete.
  it('cancel confirm dialog does not call delete API', async () => {
    const item = makeRedemption(6, 'keep-me');
    API.get.mockResolvedValue(mockListOk([item]));

    render(React.createElement(HFRedemption));

    await waitFor(() => screen.getByTestId('redemption-delete-btn-6'));

    fireEvent.click(screen.getByTestId('redemption-delete-btn-6'));

    await waitFor(() => screen.getByTestId('confirm-dialog'));

    // Cancel the dialog.
    fireEvent.click(screen.getByTestId('confirm-cancel'));

    // Dialog should be gone.
    expect(screen.queryByTestId('confirm-dialog')).toBeNull();

    // No delete call made.
    expect(API.delete).not.toHaveBeenCalled();
  });

  // 6. Pagination — 120 codes over 3 pages of 50; clicking "next" requests
  // page=2 and the range label reflects the new page.
  it('paginates through 120 codes across 3 pages', async () => {
    const page1Items = Array.from({ length: 50 }, (_, i) =>
      makeRedemption(i + 1, `code-${i + 1}`),
    );
    const page2Items = Array.from({ length: 50 }, (_, i) =>
      makeRedemption(i + 51, `code-${i + 51}`),
    );

    API.get.mockImplementation((url) => {
      const params = new URLSearchParams(url.split('?')[1]);
      const page = Number(params.get('page'));
      const items = page === 2 ? page2Items : page1Items;
      return Promise.resolve({
        data: {
          success: true,
          data: { redemptions: items, total: 120, page, page_size: 50 },
        },
      });
    });

    render(React.createElement(HFRedemption));

    await waitFor(() => {
      expect(screen.getByTestId('redemption-row-1')).toBeDefined();
    });

    // First page: prev disabled, next enabled, range label is 1-50 of 120.
    expect(screen.getByTestId('redemption-prev-btn')).toBeDisabled();
    expect(screen.getByTestId('redemption-next-btn')).not.toBeDisabled();
    expect(screen.getByTestId('redemption-range-label').textContent).toBe(
      '1–50 of 120',
    );

    fireEvent.click(screen.getByTestId('redemption-next-btn'));

    await waitFor(() => {
      expect(API.get).toHaveBeenLastCalledWith(
        expect.stringContaining('page=2'),
        expect.anything(),
      );
    });

    await waitFor(() => {
      expect(screen.getByTestId('redemption-row-51')).toBeDefined();
    });

    // Page 2 of 3 (120 rows / 50 per page = ceil(2.4) = 3 pages): prev is
    // now enabled, and the range label covers rows 51-100.
    expect(screen.getByTestId('redemption-prev-btn')).not.toBeDisabled();
    expect(screen.getByTestId('redemption-next-btn')).not.toBeDisabled();
    expect(screen.getByTestId('redemption-range-label').textContent).toBe(
      '51–100 of 120',
    );
  });

  // 6a. Deleting the only row on the last page steps back one page instead
  // of showing the empty state while codes remain.
  it('deleting the last row on the last page steps back to the previous page', async () => {
    const page1Items = Array.from({ length: 50 }, (_, i) =>
      makeRedemption(i + 1, `code-${i + 1}`),
    );
    let total = 51;
    API.get.mockImplementation((url) => {
      const params = new URLSearchParams(url.split('?')[1]);
      const page = Number(params.get('page'));
      const items =
        page === 2
          ? total > 50
            ? [makeRedemption(51, 'code-51')]
            : []
          : page1Items;
      return Promise.resolve({
        data: {
          success: true,
          data: { redemptions: items, total, page, page_size: 50 },
        },
      });
    });
    API.delete.mockImplementation(() => {
      total = 50;
      return Promise.resolve({ data: { success: true } });
    });

    render(React.createElement(HFRedemption));
    await waitFor(() => screen.getByTestId('redemption-row-1'));
    fireEvent.click(screen.getByTestId('redemption-next-btn'));
    await waitFor(() => screen.getByTestId('redemption-row-51'));

    fireEvent.click(screen.getByTestId('redemption-delete-btn-51'));
    await waitFor(() => screen.getByTestId('confirm-dialog'));
    fireEvent.click(screen.getByTestId('confirm-ok'));

    await waitFor(() => {
      expect(API.get).toHaveBeenLastCalledWith(
        expect.stringContaining('page=1'),
        expect.anything(),
      );
    });
    await waitFor(() => screen.getByTestId('redemption-row-1'));
    expect(screen.queryByTestId('redemption-empty')).toBeNull();
    expect(screen.getByTestId('redemption-range-label').textContent).toBe(
      '1–50 of 50',
    );
  });

  // 6b. API error on list fetch — does not crash.
  it('handles API error on list fetch gracefully', async () => {
    API.get.mockRejectedValue(new Error('network error'));

    // Should not throw.
    render(React.createElement(HFRedemption));

    // Loading state resolves without crash.
    await waitFor(() => {
      expect(screen.getByTestId('hf-shell')).toBeDefined();
    });
  });
});

// ─── cycle-18 L1: a failed read is not an empty one ─────────────────────────
// The list read now goes through hooks/common/useTenantRead.js. A 500 must
// render HfLoadError — not the empty state an account with nothing sees —
// and retry must re-issue the read.
describe('Redemption page — failed read', () => {
  it('renders a retryable load error instead of "No redemption codes yet" on a 500', async () => {
    let fail = true;
    API.get.mockImplementation(() =>
      fail
        ? Promise.reject({
            response: { status: 500, data: { success: false } },
          })
        : Promise.resolve(mockListOk([])),
    );

    render(React.createElement(HFRedemption));

    await waitFor(() => screen.getByTestId('redemption-load-error'));
    expect(screen.queryByTestId('redemption-empty')).toBeNull();

    fail = false;
    fireEvent.click(screen.getByTestId('redemption-retry'));
    await waitFor(() => screen.getByTestId('redemption-empty'));
    expect(screen.queryByTestId('redemption-load-error')).toBeNull();
  });
});

// ─── the create form is a real dialog (HfDialog) ─────────────────────────────

describe('create dialog semantics', () => {
  const open = async () => {
    API.get.mockResolvedValue(mockListOk([]));
    render(React.createElement(HFRedemption));
    await waitFor(() => expect(API.get).toHaveBeenCalledTimes(1));
    const opener = screen.getByTestId('redemption-create-btn');
    opener.focus();
    fireEvent.click(opener);
    await waitFor(() => screen.getByTestId('redemption-name-input'));
    return opener;
  };

  it('is a labelled modal dialog with the name field focused', async () => {
    await open();
    const dialog = screen.getByRole('dialog');
    expect(dialog).toHaveAttribute('aria-modal', 'true');
    expect(dialog).toHaveAccessibleName('New redemption code');
    expect(document.activeElement).toBe(
      screen.getByTestId('redemption-name-input'),
    );
  });

  it('closes on Escape', async () => {
    await open();
    fireEvent.keyDown(screen.getByRole('dialog'), { key: 'Escape' });
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull());
  });

  it('returns focus to the "+ new code" button after cancel', async () => {
    const opener = await open();
    fireEvent.click(screen.getByText('cancel'));
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull());
    expect(document.activeElement).toBe(opener);
  });
});
