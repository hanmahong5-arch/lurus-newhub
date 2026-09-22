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

// Vendor logos come from a ~4 MB icon pack loaded on first render; the
// page's behaviour does not depend on it, and loading it per test file is
// what made this file time out under full-suite load.
vi.mock('@lobehub/icons', () => ({}));

vi.mock('../../../helpers', () => ({
  API: {
    get: vi.fn(),
    post: vi.fn(),
    put: vi.fn(),
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

vi.mock('../../../components/common/ConfirmDialog', () => ({
  default: ({ visible, onConfirm, onCancel, title }) =>
    visible
      ? React.createElement(
          'div',
          { 'data-testid': 'confirm-dialog' },
          React.createElement('span', null, title),
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
}));

import HFChannel from './index';
import { API, showError, showSuccess } from '../../../helpers';

// ─── fixtures ────────────────────────────────────────────────────────────────

const makeChannel = (id, name) => ({
  id,
  name,
  type: 1,
  models: 'gpt-4,gpt-3.5-turbo',
  status: 1,
  group: 'default',
});

const mockListResponse = (channels) => ({
  data: {
    success: true,
    data: { channels, total: channels.length, page: 1, page_size: 100 },
  },
});

beforeEach(() => {
  API.get.mockReset();
  API.post.mockReset();
  API.put.mockReset();
  API.delete.mockReset();
  showError.mockReset();
  showSuccess.mockReset();
  window.localStorage.clear();
  window.localStorage.setItem('tenant_slug', 'acme');
});

// ─── test button shows latency on success ─────────────────────────────────────

describe('channel test button', () => {
  it('shows latency toast on success', async () => {
    const ch = makeChannel(42, 'OpenAI Main');
    API.get.mockResolvedValue(mockListResponse([ch]));
    API.post.mockResolvedValue({
      data: { success: true, latency_ms: 137 },
    });

    render(React.createElement(HFChannel));

    // Wait for channel list to render.
    await waitFor(() => screen.getByTestId('test-btn-42'));

    fireEvent.click(screen.getByTestId('test-btn-42'));

    await waitFor(() => {
      expect(API.post).toHaveBeenCalledWith('/api/v2/~/channels/42/test', {});
    });

    await waitFor(() => {
      expect(showSuccess).toHaveBeenCalledWith('Channel latency 137ms');
    });
  });

  it('shows error toast on failure', async () => {
    const ch = makeChannel(55, 'Broken Channel');
    API.get.mockResolvedValue(mockListResponse([ch]));
    API.post.mockResolvedValue({
      data: { success: false, latency_ms: 0, error: 'invalid api key' },
    });

    render(React.createElement(HFChannel));

    await waitFor(() => screen.getByTestId('test-btn-55'));
    fireEvent.click(screen.getByTestId('test-btn-55'));

    await waitFor(() => {
      expect(showError).toHaveBeenCalled();
    });
  });
});

// ─── sync modal shows model diff ─────────────────────────────────────────────

describe('sync upstream models modal', () => {
  it('shows model diff and applies selection', async () => {
    const ch = makeChannel(99, 'Sync Channel');
    API.get.mockImplementation((url) => {
      if (url.includes('/channels/99/upstream-models')) {
        return Promise.resolve({
          data: {
            success: true,
            data: {
              upstream: ['gpt-4', 'gpt-3.5-turbo', 'gpt-4o'],
              new: ['gpt-4o'],
              missing: [],
            },
          },
        });
      }
      // Default: channel list
      return Promise.resolve(mockListResponse([ch]));
    });
    API.put.mockResolvedValue({ data: { success: true } });

    render(React.createElement(HFChannel));

    // Wait for channel name to appear then expand.
    await waitFor(() => screen.getByText('Sync Channel'));
    const expandBtn = await waitFor(() =>
      screen.getAllByRole('button').find((b) => b.textContent === '▸'),
    );
    expect(expandBtn).toBeTruthy();
    fireEvent.click(expandBtn);

    // Click sync button.
    const syncBtn = await waitFor(() => screen.getByTestId('sync-btn-99'));
    fireEvent.click(syncBtn);

    // Modal should appear with new model pill.
    await waitFor(() => screen.getByTestId('sync-new-gpt-4o'));

    // Apply button should be enabled (gpt-4o is pre-selected).
    const applyBtn = screen.getByTestId('sync-apply-btn');
    expect(applyBtn).not.toBeDisabled();

    fireEvent.click(applyBtn);

    await waitFor(() => {
      expect(API.put).toHaveBeenCalledWith(
        '/api/v2/~/channels/99',
        expect.objectContaining({ models: expect.stringContaining('gpt-4o') }),
      );
    });
  });

  it('shows error when upstream fetch fails', async () => {
    const ch = makeChannel(77, 'Failing Sync');
    API.get.mockImplementation((url) => {
      if (url.includes('/channels/77/upstream-models')) {
        return Promise.resolve({
          data: { success: false, message: 'timeout' },
        });
      }
      return Promise.resolve(mockListResponse([ch]));
    });

    render(React.createElement(HFChannel));

    await waitFor(() => screen.getByText('Failing Sync'));
    const expandBtn = await waitFor(() =>
      screen.getAllByRole('button').find((b) => b.textContent === '▸'),
    );
    expect(expandBtn).toBeTruthy();
    fireEvent.click(expandBtn);

    const syncBtn = await waitFor(() => screen.getByTestId('sync-btn-77'));
    fireEvent.click(syncBtn);

    await waitFor(() => {
      expect(showError).toHaveBeenCalled();
    });
  });
});

// ─── honesty: unwired metric columns render n/a, not silent — ──────────────────

describe('channel metric columns', () => {
  it('renders n/a cells (with a reason) for qps / latency / cost', async () => {
    const ch = makeChannel(1, 'Main');
    API.get.mockResolvedValue(mockListResponse([ch]));

    render(React.createElement(HFChannel));
    await waitFor(() => screen.getByText('Main'));

    const naCells = screen.getAllByTestId('na-cell');
    // qps + p50/p95 + cost·1h = 3 honest n/a cells in the row
    expect(naCells.length).toBeGreaterThanOrEqual(3);
    expect(
      naCells.some((c) =>
        /metrics backend/i.test(c.getAttribute('title') || ''),
      ),
    ).toBe(true);
  });
});

// ─── clone channel ────────────────────────────────────────────────────────────

describe('clone channel', () => {
  it('opens a prefilled create modal and POSTs a new channel', async () => {
    const ch = makeChannel(99, 'Cloneable');
    API.get.mockResolvedValue(mockListResponse([ch]));
    API.post.mockResolvedValue({ data: { success: true } });

    render(React.createElement(HFChannel));
    await waitFor(() => screen.getByText('Cloneable'));

    const expandBtn = await waitFor(() =>
      screen.getAllByRole('button').find((b) => b.textContent === '▸'),
    );
    fireEvent.click(expandBtn);

    const cloneBtn = await waitFor(() => screen.getByTestId('clone-btn-99'));
    fireEvent.click(cloneBtn);

    // Modal opens in clone mode with the name pre-filled (" copy" suffix).
    await waitFor(() => screen.getByText(/Clone · Cloneable/));
    expect(screen.getByDisplayValue('Cloneable copy')).toBeTruthy();

    // Submit → POST (create), not PUT (edit).
    fireEvent.click(screen.getByText('create channel'));
    await waitFor(() => {
      expect(API.post).toHaveBeenCalledWith(
        '/api/v2/~/channels',
        expect.objectContaining({ name: 'Cloneable copy' }),
      );
    });
  });
});

// ─── provider type is named, not a bare integer ──────────────────────────────

describe('channel provider type', () => {
  it('names the type in the list instead of printing its id', async () => {
    API.get.mockResolvedValue(
      mockListResponse([{ ...makeChannel(7, 'Suno one'), type: 36 }]),
    );
    render(React.createElement(HFChannel));
    await waitFor(() => screen.getByText('Suno one'));
    const cell = screen.getByTestId('channel-type-label');
    expect(cell.textContent).toContain('Suno');
    expect(cell.textContent).not.toBe('36');
  });

  it('offers named types in the create form, keeping the numeric id as the value', async () => {
    const ch = makeChannel(99, 'Cloneable');
    API.get.mockResolvedValue(mockListResponse([ch]));
    API.post.mockResolvedValue({ data: { success: true } });
    render(React.createElement(HFChannel));
    await waitFor(() => screen.getByText('Cloneable'));
    fireEvent.click(
      await waitFor(() =>
        screen.getAllByRole('button').find((b) => b.textContent === '▸'),
      ),
    );
    fireEvent.click(await waitFor(() => screen.getByTestId('clone-btn-99')));
    const select = await waitFor(() =>
      screen.getByTestId('channel-type-select'),
    );
    const opt = Array.from(select.options).find((o) => o.value === '14');
    expect(opt.textContent).toMatch(/Claude/);
    fireEvent.change(select, { target: { value: '14' } });
    fireEvent.click(screen.getByText('create channel'));
    await waitFor(() => expect(API.post).toHaveBeenCalled());
    expect(Number(API.post.mock.calls[0][1].type)).toBe(14);
  });
});

// ─── batch test all enabled ───────────────────────────────────────────────────

describe('test all enabled channels', () => {
  it('tests only enabled channels with bounded concurrency', async () => {
    const channels = [
      makeChannel(1, 'A'),
      makeChannel(2, 'B'),
      { ...makeChannel(3, 'C'), status: 2 }, // disabled — must be skipped
    ];
    API.get.mockResolvedValue(mockListResponse(channels));
    API.post.mockResolvedValue({ data: { success: true, latency_ms: 100 } });

    render(React.createElement(HFChannel));
    await waitFor(() => screen.getByText('A'));

    fireEvent.click(screen.getByTestId('batch-test-all-btn'));

    await waitFor(() => {
      const testCalls = API.post.mock.calls.filter(([u]) =>
        /\/channels\/\d+\/test$/.test(u),
      );
      expect(testCalls.length).toBe(2);
    });
    expect(API.post).toHaveBeenCalledWith('/api/v2/~/channels/1/test', {});
    expect(API.post).toHaveBeenCalledWith('/api/v2/~/channels/2/test', {});
  });
});

// ─── filters ──────────────────────────────────────────────────────────────────

describe('channel filters', () => {
  it('exposes keyword/group/status filters and sends keyword to the backend', async () => {
    const ch = makeChannel(1, 'Main');
    API.get.mockResolvedValue(mockListResponse([ch]));

    render(React.createElement(HFChannel));
    await waitFor(() => screen.getByText('Main'));

    expect(screen.getByTestId('channel-filter-keyword')).toBeTruthy();
    expect(screen.getByTestId('channel-filter-group')).toBeTruthy();
    expect(screen.getByTestId('channel-filter-status')).toBeTruthy();

    fireEvent.change(screen.getByTestId('channel-filter-keyword'), {
      target: { value: 'openai' },
    });
    fireEvent.click(screen.getByText('search'));

    await waitFor(() => {
      const calls = API.get.mock.calls.map(([u]) => u);
      expect(calls.some((u) => u.includes('keyword=openai'))).toBe(true);
    });
  });

  it('status filter hides non-matching channels client-side', async () => {
    const channels = [
      makeChannel(1, 'EnabledChan'),
      { ...makeChannel(2, 'DisabledChan'), status: 2 },
    ];
    API.get.mockResolvedValue(mockListResponse(channels));

    render(React.createElement(HFChannel));
    await waitFor(() => screen.getByText('EnabledChan'));
    expect(screen.getByText('DisabledChan')).toBeTruthy();

    fireEvent.change(screen.getByTestId('channel-filter-status'), {
      target: { value: 'disabled' },
    });

    await waitFor(() => {
      expect(screen.queryByText('EnabledChan')).toBeNull();
    });
    expect(screen.getByText('DisabledChan')).toBeTruthy();
  });
});
