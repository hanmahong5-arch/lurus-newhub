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
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { fireEvent, render, screen, waitFor } from '@testing-library/react';

vi.mock('../../../helpers', () => ({
  API: { get: vi.fn() },
  isRoot: vi.fn(() => false),
}));

vi.mock('../../../components/hifi/HFShell', () => ({
  default: ({ children }) =>
    React.createElement('div', { 'data-testid': 'hf-shell' }, children),
}));

import HFRankings from './Rankings';
import { API, isRoot } from '../../../helpers';
import i18n from '../../../i18n/i18n';

const payload = (over = {}) => ({
  data: {
    success: true,
    data: {
      by: 'model',
      window: { start: 1000, end: 2000 },
      cached_at: 1234567890,
      rows: [
        {
          name: 'gpt-4o',
          rank: 1,
          rank_delta: 1,
          is_new: false,
          requests: 30,
          total_tokens: 900,
          quota: 4_500_000,
          token_share_pct: 75,
          quota_share_pct: 75,
          requests_growth_pct: 20,
        },
        {
          name: 'deepseek-chat',
          rank: 2,
          rank_delta: 0,
          is_new: true,
          requests: 10,
          total_tokens: 300,
          quota: 1_500_000,
          token_share_pct: 25,
          quota_share_pct: 25,
          requests_growth_pct: null,
        },
      ],
      ...over,
    },
  },
});

beforeEach(() => {
  API.get.mockReset();
  window.localStorage.clear();
  window.localStorage.setItem('tenant_slug', 'acme');
});

describe('Rankings page', () => {
  it('renders rank/trend/share rows from the mocked payload (not literals)', async () => {
    API.get.mockResolvedValue(payload());
    render(<HFRankings />);

    await waitFor(() => {
      expect(screen.getAllByTestId('rankings-row')).toHaveLength(2);
    });
    expect(screen.getByText('gpt-4o')).toBeInTheDocument();
    expect(screen.getByText('deepseek-chat')).toBeInTheDocument();
    // Top row moved up 1 (rank_delta=1) -> ▲1; second row is_new -> "new".
    expect(screen.getByText('▲1')).toBeInTheDocument();
    expect(screen.getByText('new')).toBeInTheDocument();
  });

  it('calls the tenant-scoped rankings endpoint with the resolved slug', async () => {
    API.get.mockResolvedValue(payload());
    render(<HFRankings />);

    await waitFor(() => {
      expect(API.get).toHaveBeenCalled();
    });
    const [url] = API.get.mock.calls[0];
    expect(url).toBe('/api/v2/~/analytics/rankings?by=model&hours=24');
  });

  it('switching to the vendor tab re-fetches with by=vendor', async () => {
    API.get.mockResolvedValue(payload({ by: 'vendor' }));
    render(<HFRankings />);
    await waitFor(() => screen.getByTestId('rankings-by-vendor'));

    fireEvent.click(screen.getByTestId('rankings-by-vendor'));

    await waitFor(() => {
      const lastCall = API.get.mock.calls[API.get.mock.calls.length - 1];
      expect(lastCall[0]).toContain('by=vendor');
    });
  });

  it('renders the forbidden panel on a 403', async () => {
    API.get.mockRejectedValue({ response: { status: 403 } });
    render(<HFRankings />);

    await waitFor(() => {
      expect(screen.getByText(/Admin access required/i)).toBeInTheDocument();
    });
  });

  it('renders the empty state when rows is empty', async () => {
    API.get.mockResolvedValue(payload({ rows: [] }));
    render(<HFRankings />);

    await waitFor(() => {
      expect(screen.getByTestId('rankings-empty')).toBeInTheDocument();
    });
  });

  it('clears the previous rows and shows an error on a 429 from a later fetch', async () => {
    API.get.mockResolvedValueOnce(payload());
    render(<HFRankings />);
    await waitFor(() => {
      expect(screen.getAllByTestId('rankings-row')).toHaveLength(2);
    });

    API.get.mockRejectedValueOnce({ response: { status: 429 } });
    fireEvent.click(screen.getByTestId('rankings-hours-6'));

    await waitFor(() => {
      expect(screen.getByTestId('rankings-error')).toBeInTheDocument();
    });
    // The stale table from the previous (hours=24) fetch must not survive
    // under the newly selected (hours=6) tab.
    expect(screen.queryByTestId('rankings-row')).toBeNull();
    expect(screen.queryByTestId('rankings-table')).toBeNull();
  });

  // Lock for cycle-7 findings round 2 item 9: a transient 429/5xx must not
  // strip the tab/preset row, otherwise the only recovery is a full page
  // reload (which also resets to model/24h). The controls — including the
  // by=vendor tab and the hours=6 preset just clicked — must stay usable
  // while the error panel is showing.
  it('keeps the tab/preset controls usable after a 429 (no dead-end panel)', async () => {
    API.get.mockResolvedValueOnce(payload());
    render(<HFRankings />);
    await waitFor(() => {
      expect(screen.getAllByTestId('rankings-row')).toHaveLength(2);
    });

    API.get.mockRejectedValueOnce({ response: { status: 429 } });
    fireEvent.click(screen.getByTestId('rankings-hours-6'));

    await waitFor(() => {
      expect(screen.getByTestId('rankings-error')).toBeInTheDocument();
    });
    expect(screen.getByTestId('rankings-by-vendor')).toBeInTheDocument();
    expect(screen.getByTestId('rankings-by-model')).toBeInTheDocument();
    expect(screen.getByTestId('rankings-hours-24')).toBeInTheDocument();

    // The controls are not just present but still wired: clicking the
    // vendor tab from the error state must trigger a new fetch.
    API.get.mockResolvedValueOnce(payload({ by: 'vendor' }));
    fireEvent.click(screen.getByTestId('rankings-by-vendor'));
    await waitFor(() => {
      const lastCall = API.get.mock.calls[API.get.mock.calls.length - 1];
      expect(lastCall[0]).toContain('by=vendor');
    });
  });

  // Lock for cycle-7 findings round 2 item 8: the console's hour presets
  // must stay in lockstep with the backend's snap targets
  // (rankingsHourPresets in v2_analytics_rankings.go). There is no shared
  // import across the Go/JS boundary, so this pins the literal list on
  // this side; a change to either side without the other must be caught
  // by a human reading this comment, and this test at least catches an
  // accidental edit to the JS list alone.
  it('renders exactly the backend snap presets {1,6,24,168,720}', async () => {
    API.get.mockResolvedValue(payload());
    render(<HFRankings />);
    await waitFor(() => {
      expect(screen.getAllByTestId('rankings-row')).toHaveLength(2);
    });
    const backendRankingsHourPresets = [1, 6, 24, 168, 720];
    for (const h of backendRankingsHourPresets) {
      expect(screen.getByTestId(`rankings-hours-${h}`)).toBeInTheDocument();
    }
    // No extra preset buttons beyond the five above.
    expect(
      screen.queryAllByTestId(/^rankings-hours-/).map((el) => el.textContent),
    ).toHaveLength(backendRankingsHourPresets.length);
  });

  it('renders the API-provided total_tokens, not a sum of only the returned (max 20) rows', async () => {
    // rows sum to 900+300=1200; the API's own window total (42) must win.
    API.get.mockResolvedValue(payload({ total_tokens: 42 }));
    render(<HFRankings />);

    await waitFor(() => {
      expect(screen.getAllByTestId('rankings-row')).toHaveLength(2);
    });
    expect(screen.getByText(/total tokens in window: 42/)).toBeInTheDocument();
  });

  // Proves the Trend cell's "new"/flat-dash text is a real translation call
  // (tr(key, fallback)), not a bare literal: a literal string would render
  // identically in every language, but this asserts the zh-locale text
  // differs from the English fallback used elsewhere in this file.
  describe('Trend cell goes through i18n, not a bare literal', () => {
    afterEach(async () => {
      await i18n.changeLanguage('en');
    });

    it('renders the zh translation for is_new under the zh locale', async () => {
      await i18n.changeLanguage('zh');
      API.get.mockResolvedValue(payload());
      render(<HFRankings />);

      await waitFor(() => {
        expect(screen.getAllByTestId('rankings-row')).toHaveLength(2);
      });
      expect(screen.getByText('新上榜')).toBeInTheDocument();
      expect(screen.queryByText('new')).toBeNull();
    });

    // Lock for cycle-7 findings round 2 item 4: the growth cell's "no
    // previous-window baseline" glyph must be a real tr() call, not the
    // bare '—' literal it used to be — a bare literal renders identically
    // under every locale, this does not (zh: 无基线, en fallback: —).
    it('renders the zh translation for the growth cell "no baseline" case', async () => {
      await i18n.changeLanguage('zh');
      // deepseek-chat's requests_growth_pct is null in the shared fixture.
      API.get.mockResolvedValue(payload());
      render(<HFRankings />);

      await waitFor(() => {
        expect(screen.getAllByTestId('rankings-row')).toHaveLength(2);
      });
      expect(screen.getByText('无基线')).toBeInTheDocument();
      expect(screen.queryByText('—')).toBeNull();
    });
  });

  // Cycle-9 plan L7: `by=group` is a third accepted dimension alongside
  // model/vendor — the backend now groups on the logs table's `group`
  // column (internal/adapter/repo/analytics.go's getGroupUsageTotals).
  describe('by=group dimension', () => {
    it('renders a "by group" tab that re-fetches with by=group', async () => {
      API.get.mockResolvedValue(payload());
      render(<HFRankings />);
      await waitFor(() => screen.getByTestId('rankings-by-group'));

      API.get.mockResolvedValueOnce(payload({ by: 'group' }));
      fireEvent.click(screen.getByTestId('rankings-by-group'));

      await waitFor(() => {
        const lastCall = API.get.mock.calls[API.get.mock.calls.length - 1];
        expect(lastCall[0]).toContain('by=group');
      });
    });

    it('renders rows returned under by=group (e.g. a labelled "(ungrouped)" bucket)', async () => {
      API.get.mockResolvedValueOnce(payload());
      render(<HFRankings />);
      await waitFor(() => screen.getByTestId('rankings-by-group'));

      API.get.mockResolvedValueOnce(
        payload({
          by: 'group',
          rows: [
            {
              name: '(ungrouped)',
              rank: 1,
              rank_delta: 0,
              is_new: true,
              requests: 5,
              total_tokens: 400,
              quota: 2_000_000,
              token_share_pct: 100,
              quota_share_pct: 100,
              requests_growth_pct: null,
            },
          ],
        }),
      );
      fireEvent.click(screen.getByTestId('rankings-by-group'));

      await waitFor(() => {
        expect(screen.getByText('(ungrouped)')).toBeInTheDocument();
      });
    });
  });
});

// GET /api/v2/admin/analytics/rankings (GetRankingsV2, RootJWTAuth, optional
// tenant_id) shipped with no console consumer: the leaderboard a root operator
// saw was silently scoped to one tenant, with nothing in the UI saying so, and
// the platform-wide view the server could already produce was unreachable.
describe('Rankings — cross-tenant scope (root only)', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    API.get.mockResolvedValue({
      data: {
        success: true,
        data: { rows: [], cached_at: 0, total_tokens: 0 },
      },
    });
  });

  it('offers no scope control to a non-root user and reads the tenant route', async () => {
    isRoot.mockReturnValue(false);

    render(React.createElement(HFRankings));

    await waitFor(() => expect(API.get).toHaveBeenCalled());
    expect(screen.queryByTestId('rankings-scope-all')).toBeNull();
    expect(API.get.mock.calls[0][0]).toContain('/analytics/rankings');
    expect(API.get.mock.calls[0][0]).not.toContain('/admin/analytics/rankings');
  });

  it('switches root to the platform-wide admin route and back', async () => {
    isRoot.mockReturnValue(true);

    render(React.createElement(HFRankings));

    await waitFor(() => expect(API.get).toHaveBeenCalled());
    // Default scope is still the tenant route — the broader view is opt-in.
    expect(API.get.mock.calls[0][0]).not.toContain('/admin/analytics/rankings');

    fireEvent.click(screen.getByTestId('rankings-scope-all'));
    await waitFor(() =>
      expect(
        API.get.mock.calls.some((c) =>
          c[0].startsWith('/api/v2/admin/analytics/rankings'),
        ),
      ).toBe(true),
    );

    const before = API.get.mock.calls.length;
    fireEvent.click(screen.getByTestId('rankings-scope-tenant'));
    await waitFor(() =>
      expect(API.get.mock.calls.length).toBeGreaterThan(before),
    );
    const last = API.get.mock.calls[API.get.mock.calls.length - 1][0];
    expect(last).not.toContain('/admin/analytics/rankings');
  });

  it('carries the by and hours params onto the admin route unchanged', async () => {
    isRoot.mockReturnValue(true);

    render(React.createElement(HFRankings));
    await waitFor(() => expect(API.get).toHaveBeenCalled());

    fireEvent.click(screen.getByTestId('rankings-by-group'));
    fireEvent.click(screen.getByTestId('rankings-scope-all'));

    await waitFor(() =>
      expect(
        API.get.mock.calls.some((c) =>
          c[0].startsWith('/api/v2/admin/analytics/rankings'),
        ),
      ).toBe(true),
    );
    const adminCall = API.get.mock.calls
      .filter((c) => c[0].startsWith('/api/v2/admin/analytics/rankings'))
      .pop()[0];
    const qs = new URLSearchParams(adminCall.split('?')[1]);
    expect(qs.get('by')).toBe('group');
    expect(qs.get('hours')).toBeTruthy();
  });
});
