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

// Regression: every USD⇄quota conversion on the Token page must go through the
// operator's live quota_per_unit. The page used to write caps with a hardcoded
// 500000 divisor while reading them back through the live rate, so on any
// deployment with a non-default unit price the number you typed and the number
// you saw disagreed by exactly that ratio.
import React from 'react';
import { describe, it, expect, vi, beforeEach } from 'vitest';
import { fireEvent, render, screen, waitFor } from '@testing-library/react';

vi.mock('../../../helpers', () => ({
  // The page resolves the relay host from the server it is served by.
  getServerAddress: () => 'https://hub.example.test',
  API: {
    get: vi.fn(),
    post: vi.fn(),
    put: vi.fn(),
    delete: vi.fn(),
  },
  showError: vi.fn(),
  showSuccess: vi.fn(),
}));

vi.mock('react-router-dom', () => ({
  useNavigate: () => vi.fn(),
}));

vi.mock('react-i18next', () => ({
  useTranslation: () => ({
    // String defaultValue wins, as in real i18next. Interpolation vars are
    // appended instead of dropped so figures passed only through a translated
    // template (the header summary) stay observable in the DOM.
    t: (k, d, opts) => {
      if (typeof d === 'string') return d;
      const vars = typeof d === 'object' && d !== null ? d : opts;
      if (!vars) return k;
      return `${k}|${Object.entries(vars)
        .map(([a, b]) => `${a}=${b}`)
        .join(',')}`;
    },
  }),
  initReactI18next: { type: '3rdParty', init: () => {} },
}));

vi.mock('../../../components/hifi/HFShell', () => ({
  default: ({ children, actions }) =>
    React.createElement(
      'div',
      { 'data-testid': 'hf-shell' },
      React.createElement('div', null, actions),
      children,
    ),
}));

vi.mock('../../../components/common/ConfirmDialog', () => ({
  default: ({ visible, onConfirm }) =>
    visible
      ? React.createElement(
          'button',
          { 'data-testid': 'confirm-ok', onClick: onConfirm },
          'confirm',
        )
      : null,
}));

import HFToken from './index';
import { API } from '../../../helpers';

// Deliberately not 500000 — the point is that the page must not assume the
// default. 1000 units == $1 here.
const UNIT = 1000;

const makeToken = (overrides = {}) => ({
  id: 1,
  name: 'prod',
  key: 'abcd1234efgh',
  status: 1,
  unlimited_quota: false,
  used_quota: 500,
  remain_quota: 1500,
  expired_time: -1,
  created_time: Math.floor(Date.now() / 1000) - 3600,
  accessed_time: Math.floor(Date.now() / 1000) - 60,
  ...overrides,
});

const wireGet = (tokens) => {
  API.get.mockImplementation((url) => {
    if (String(url).includes('/projects')) {
      return Promise.resolve({ data: { success: true, data: { items: [] } } });
    }
    return Promise.resolve({
      data: { success: true, data: { items: tokens } },
    });
  });
};

beforeEach(() => {
  API.get.mockReset();
  API.post.mockReset();
  API.put.mockReset();
  API.delete.mockReset();
  window.localStorage.clear();
  window.localStorage.setItem('tenant_slug', 'acme');
  window.localStorage.setItem('quota_per_unit', String(UNIT));
  Object.defineProperty(navigator, 'clipboard', {
    configurable: true,
    value: { writeText: vi.fn().mockResolvedValue(undefined) },
  });
});

describe('Token page — USD caps use the live quota_per_unit', () => {
  it('creates a token with remain_quota at the operator rate', async () => {
    wireGet([]);
    API.post.mockResolvedValue({
      data: { success: true, data: { key: 'sk-new' } },
    });

    render(<HFToken />);
    await waitFor(() => screen.getByText('+ new token'));

    fireEvent.click(screen.getByText('+ new token'));
    fireEvent.change(screen.getByTestId('token-name-input'), {
      target: { value: 'capped' },
    });
    // The cap field only exists once the token is not unlimited.
    fireEvent.click(
      screen.getByText('unlimited quota').parentElement.querySelector('input'),
    );
    fireEvent.change(screen.getByPlaceholderText('200'), {
      target: { value: '2' },
    });
    fireEvent.click(screen.getByTestId('token-create-submit'));

    await waitFor(() => {
      const call = API.post.mock.calls.find(([url]) =>
        String(url).endsWith('/tokens'),
      );
      expect(call).toBeTruthy();
      // $2 at 1000 units/$ = 2000, not the 1_000_000 a hardcoded 500000 gives.
      expect(call[1].remain_quota).toBe(2 * UNIT);
      expect(call[1].unlimited_quota).toBe(false);
    });
  });

  it('saves an edited cap at the operator rate', async () => {
    const token = makeToken();
    wireGet([token]);
    API.put.mockResolvedValue({ data: { success: true } });

    render(<HFToken />);
    await waitFor(() => screen.getByText('monthly cap'));

    // The "monthly cap" settings row: label, value, edit button.
    const editBtn = screen
      .getByText('monthly cap')
      .parentElement.querySelector('button');
    fireEvent.click(editBtn);

    // The inline editor is seeded with the current cap, $2.00 at this rate.
    const editor = screen.getByDisplayValue('$2.00');
    fireEvent.change(editor, { target: { value: '5' } });
    fireEvent.keyDown(editor, { key: 'Enter' });

    await waitFor(() => {
      expect(API.put).toHaveBeenCalledTimes(1);
    });
    const [, body] = API.put.mock.calls[0];
    // $5 total = 5000 units, minus the 500 already used → 4500 remaining.
    expect(body.remain_quota).toBe(5 * UNIT - token.used_quota);
    expect(body.unlimited_quota).toBe(false);
  });

  it('totals used and capped spend at the operator rate', async () => {
    wireGet([
      makeToken({ id: 1, name: 'one', used_quota: 500, remain_quota: 1500 }),
      makeToken({ id: 2, name: 'two', used_quota: 1000, remain_quota: 2000 }),
    ]);

    render(<HFToken />);

    // Header summary: used = (500 + 1000) / 1000 = 1.50, and the cap suffix
    // ` / $<total>` = (2000 + 3000) / 1000 = 5.00. Both figures are unique to
    // the reducers — no per-token cap cell reads $5.00.
    await waitFor(() => {
      const summary = screen.getByText(/console\.token\.summary/);
      expect(summary.textContent).toContain('used=1.50');
      expect(summary.textContent).toContain('/ $5.00');
    });
  });
});
