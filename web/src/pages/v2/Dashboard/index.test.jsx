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
import { render, screen, waitFor } from '@testing-library/react';

// ─── mocks ───────────────────────────────────────────────────────────────────

vi.mock('../../../helpers', () => ({
  // The onboarding curl block resolves the relay host at render time.
  getServerAddress: () => 'https://hub.example.test',
  API: {
    get: vi.fn(),
  },
  showError: vi.fn(),
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

import HFDashboard from './index';
import { API } from '../../../helpers';

// ─── fixtures ────────────────────────────────────────────────────────────────

// Backend /api/v2/:slug/user/me response — all snake_case keys
const makeMe = (overrides = {}) => ({
  username: 'testuser',
  display_name: 'Test User',
  used_quota: 250000, // snake_case — NOT usedQuota
  remaining_quota: 750000, // snake_case — NOT remainingQuota
  token_count: 3, // snake_case — NOT tokenCount
  request_count: 42, // snake_case — NOT requestCount
  ...overrides,
});

// Backend /api/v2/:slug/logs response — items use snake_case keys
const makeLog = (overrides = {}) => ({
  type: 2,
  model_name: 'gpt-4o', // snake_case — NOT modelName
  quota: 1000,
  total_latency_ms: 150, // snake_case — NOT totalLatencyMs
  created_at: Math.floor(Date.now() / 1000) - 60, // snake_case — NOT createdAt
  ...overrides,
});

beforeEach(() => {
  API.get.mockReset();
  window.localStorage.clear();
  window.localStorage.setItem('tenant_slug', 'acme');
});

// ─── Dashboard KPI card fixture tests ────────────────────────────────────────

describe('Dashboard page — KPI cards read lowercase keys', () => {
  it('renders total spend from used_quota (snake_case)', async () => {
    API.get.mockResolvedValue({
      data: { success: true, data: makeMe({ used_quota: 500000 }) },
    });

    render(React.createElement(HFDashboard));

    // Total spend KPI: $1.00 from 500000 / 500000 = 1.00
    await waitFor(() => screen.getByText('total spend'));
    await waitFor(() => screen.getByText('$1.00'));
  });

  it('renders active tokens from token_count (snake_case)', async () => {
    API.get.mockResolvedValue({
      data: { success: true, data: makeMe({ token_count: 7 }) },
    });

    render(React.createElement(HFDashboard));

    await waitFor(() => screen.getByText('active tokens'));
    await waitFor(() => screen.getByText('7'));
  });

  it('renders total requests from request_count (snake_case)', async () => {
    API.get.mockResolvedValue({
      data: { success: true, data: makeMe({ request_count: 99 }) },
    });

    render(React.createElement(HFDashboard));

    await waitFor(() => screen.getByText('total requests'));
    await waitFor(() => screen.getByText('99'));
  });

  it('renders remaining quota from remaining_quota (snake_case)', async () => {
    // remaining_quota = 1000000 → $2.00
    API.get.mockResolvedValue({
      data: { success: true, data: makeMe({ remaining_quota: 1000000 }) },
    });

    render(React.createElement(HFDashboard));

    await waitFor(() => screen.getByText('remaining quota'));
    await waitFor(() => screen.getByText('$2.00'));
  });

  it('renders infinity remaining for negative remaining_quota (unlimited plan)', async () => {
    API.get.mockResolvedValue({
      data: { success: true, data: makeMe({ remaining_quota: -1 }) },
    });

    render(React.createElement(HFDashboard));

    await waitFor(() => screen.getByText('remaining quota'));
    await waitFor(() => screen.getByText('∞'));
  });
});

describe('Dashboard page — user/me + logs fetched with correct URLs', () => {
  it('fetches /api/v2/acme/user/me and /api/v2/acme/logs', async () => {
    API.get
      .mockResolvedValueOnce({ data: { success: true, data: makeMe() } })
      .mockResolvedValueOnce({
        // Real backend shape: GET /logs returns { logs: [...] }.
        data: { success: true, data: { logs: [makeLog()] } },
      });

    render(React.createElement(HFDashboard));

    await waitFor(() => {
      const calls = API.get.mock.calls.map((c) => c[0]);
      expect(calls.some((u) => u.includes('/api/v2/acme/user/me'))).toBe(true);
      expect(calls.some((u) => u.includes('/api/v2/acme/logs'))).toBe(true);
    });
  });

  // Regression guard: the dashboard must derive realtime KPIs from the `.logs`
  // array (the real backend key). The prior code read `.items`, so the latency
  // percentiles were always empty against production.
  it('derives realtime latency KPIs from the .logs array', async () => {
    const me = makeMe({ token_count: 3 });
    const now = Math.floor(Date.now() / 1000);
    const logs = [
      makeLog({ type: 2, total_latency_ms: 100, created_at: now - 10 }),
      makeLog({ type: 2, total_latency_ms: 200, created_at: now - 20 }),
    ];
    API.get.mockImplementation((url) => {
      if (url.includes('/user/me')) {
        return Promise.resolve({ data: { success: true, data: me } });
      }
      return Promise.resolve({
        data: { success: true, data: { logs, total: logs.length } },
      });
    });

    render(React.createElement(HFDashboard));

    // p50 of [100, 200] = 150ms — only renders if `.logs` was consumed.
    await waitFor(() => {
      expect(screen.getByText('150ms')).toBeTruthy();
    });
  });

  it('shows display_name (snake_case) in the h1 heading', async () => {
    API.get.mockResolvedValue({
      data: { success: true, data: makeMe({ display_name: 'Alice Smith' }) },
    });

    render(React.createElement(HFDashboard));

    await waitFor(() => screen.getByText(/Alice Smith/));
  });

  it('shows onboarding block when token_count is 0', async () => {
    API.get.mockResolvedValue({
      data: { success: true, data: makeMe({ token_count: 0 }) },
    });

    render(React.createElement(HFDashboard));

    await waitFor(() => screen.getByText(/0 tokens/));
  });

  // L1 (cycle-11): the onboarding curl's "model" field used to be a
  // hardcoded literal — now it's the caller's first OpenAI-wire routable
  // model, fetched from GET .../models/routable.
  it('onboarding curl uses the first OpenAI-wire routable model, not a literal', async () => {
    API.get.mockImplementation((url) => {
      const u = String(url);
      if (u.includes('/models/routable')) {
        return Promise.resolve({
          data: {
            success: true,
            data: {
              items: [
                {
                  id: 'rt-anthropic-only',
                  owned_by: 'Vendor X',
                  supported_endpoint_types: ['anthropic'],
                },
                {
                  id: 'rt-openai-pick',
                  owned_by: 'Vendor Y',
                  supported_endpoint_types: ['openai'],
                },
              ],
            },
          },
        });
      }
      return Promise.resolve({
        data: { success: true, data: makeMe({ token_count: 0 }) },
      });
    });

    render(React.createElement(HFDashboard));

    await waitFor(() =>
      expect(screen.getByText(/"model": "rt-openai-pick"/)).toBeTruthy(),
    );
    // The anthropic-only fixture model must not have been picked — proves
    // the wire filter, not just "some routable model", drove the choice.
    expect(screen.queryByText(/rt-anthropic-only/)).toBeNull();
  });

  // resolved && !model -> honest empty state, no <pre> that must fail.
  it('renders an honest empty state (no <pre>) when nothing is routable', async () => {
    API.get.mockImplementation((url) => {
      const u = String(url);
      if (u.includes('/models/routable')) {
        return Promise.resolve({
          data: { success: true, data: { items: [] } },
        });
      }
      return Promise.resolve({
        data: { success: true, data: makeMe({ token_count: 0 }) },
      });
    });

    render(React.createElement(HFDashboard));

    await waitFor(() =>
      expect(screen.getByTestId('dashboard-onboarding-no-models')).toBeTruthy(),
    );
    expect(document.querySelector('pre')).toBeNull();
  });

  // A rejected /models/routable fetch is NOT proof the tenant has nothing
  // to route — it is proof only that we could not ask. Must render its own
  // load-failed line, never the "add a channel" copy, which would send a
  // brand-new customer chasing a channel that may well already exist.
  it('renders an onboarding load-failed state (not the empty state) when the routable fetch errors', async () => {
    API.get.mockImplementation((url) => {
      const u = String(url);
      if (u.includes('/models/routable')) {
        return Promise.reject(new Error('network down'));
      }
      return Promise.resolve({
        data: { success: true, data: makeMe({ token_count: 0 }) },
      });
    });

    render(React.createElement(HFDashboard));

    await waitFor(() =>
      expect(
        screen.getByTestId('dashboard-onboarding-load-failed'),
      ).toBeTruthy(),
    );
    expect(screen.queryByTestId('dashboard-onboarding-no-models')).toBeNull();
    expect(document.querySelector('pre')).toBeNull();
  });

  it('cost-by-model rows deep-link to the log page filtered to that model', async () => {
    const now = Math.floor(Date.now() / 1000);
    API.get.mockImplementation((url) => {
      if (url.includes('/user/me')) {
        return Promise.resolve({ data: { success: true, data: makeMe() } });
      }
      return Promise.resolve({
        data: {
          success: true,
          data: {
            logs: [
              makeLog({
                type: 2,
                model_name: 'model/x-1',
                quota: 5000,
                created_at: now - 10,
              }),
            ],
            total: 1,
          },
        },
      });
    });

    // Capture href assignments without leaving jsdom.
    const prevLocation = window.location;
    let assignedHref = '';
    Object.defineProperty(window, 'location', {
      configurable: true,
      value: {
        ...prevLocation,
        set href(v) {
          assignedHref = v;
        },
        get href() {
          return assignedHref;
        },
      },
    });
    try {
      const { fireEvent } = await import('@testing-library/react');
      render(React.createElement(HFDashboard));

      const row = await waitFor(() => screen.getByTestId('cost-model-row-0'));
      fireEvent.click(row);
      // The '/' in the model name must be encoded, or the link lands on a
      // nonexistent nested route instead of a query param.
      expect(assignedHref).toBe('/console/v2/log?model_name=model%2Fx-1');
    } finally {
      Object.defineProperty(window, 'location', {
        configurable: true,
        value: prevLocation,
      });
    }
  });

  it('does NOT show onboarding when token_count > 0', async () => {
    API.get.mockResolvedValue({
      data: { success: true, data: makeMe({ token_count: 2 }) },
    });

    render(React.createElement(HFDashboard));

    await waitFor(() => screen.queryByText('total spend') !== null);
    expect(screen.queryByText(/0 tokens/)).toBeNull();
  });
});

// ─── L1: /api/data/self/ + /api/status + /api/uptime/status ──────────────────
//
// Routes each API.get call by URL so the KPI-strip fetches (user/me, logs)
// and the new L1 fetches (data/self, status, uptime/status) can each be wired
// independently in a single test.
const wireDashboard = ({
  me = makeMe(),
  logsPayload = { logs: [], total: 0 },
  quota = { data: { success: true, data: [] } },
  status = { data: { success: true, data: {} } },
  uptime = { data: { success: true, data: [] } },
} = {}) => {
  API.get.mockImplementation((url) => {
    const u = String(url);
    if (u.includes('/user/me')) {
      return Promise.resolve({ data: { success: true, data: me } });
    }
    if (u.includes('/api/data/self/')) {
      return Promise.resolve(quota);
    }
    if (u.includes('/api/uptime/status')) {
      return Promise.resolve(uptime);
    }
    if (u.includes('/api/status')) {
      return Promise.resolve(status);
    }
    if (u.includes('/logs')) {
      return Promise.resolve({ data: { success: true, data: logsPayload } });
    }
    return Promise.resolve({ data: { success: true, data: {} } });
  });
};

// entity.QuotaData (internal/domain/entity/usedata.go): one row per
// (user, model, hour-bucket) — id/user_id/username are present on the wire
// but irrelevant to the trend/distribution aggregation.
const makeQuotaRow = (overrides = {}) => ({
  id: 1,
  user_id: 1,
  username: 'testuser',
  model_name: 'gpt-4o',
  created_at: Math.floor(Date.now() / 1000),
  token_used: 100,
  count: 1,
  quota: 100000,
  ...overrides,
});

// enable_data_export is off by default in the wireDashboard() status fixture
// ({} has no such key), so every test in this describe block that expects the
// trend/model-distribution panels to be present must opt back in explicitly —
// those two panels are gated on this flag (misc.go:165 common.DataExportEnabled).
const EXPORT_ON_STATUS = {
  data: { success: true, data: { enable_data_export: true } },
};

describe('Dashboard page — usage trend + model distribution (/api/data/self/)', () => {
  // skipErrorHandler:true on this call would defeat the Layer-C 401
  // self-heal in helpers/api.js (config.skipErrorHandler short-circuits the
  // interceptor before the self-heal branch runs). This file mocks
  // '../../../helpers' wholesale, so it cannot exercise that interceptor —
  // this asserts the one thing it CAN see: the call-site contract that makes
  // the self-heal reachable in the first place.
  it('calls /api/data/self/ without skipErrorHandler (401 self-heal must stay reachable)', async () => {
    wireDashboard();

    render(React.createElement(HFDashboard));

    await waitFor(() => screen.getByText('total spend'));
    const call = API.get.mock.calls.find((c) =>
      String(c[0]).includes('/api/data/self/'),
    );
    expect(call).toBeTruthy();
    expect(call[1]?.skipErrorHandler).not.toBe(true);
  });

  it('renders a non-empty trend series and model distribution from a recorded payload', async () => {
    const now = Math.floor(Date.now() / 1000);
    const twoDaysAgo = now - 2 * 24 * 60 * 60;
    const oneDayAgo = now - 1 * 24 * 60 * 60;
    // Day 1: model-a 300000 + 400000 = 700000. Day 2: model-b 300000.
    // Total 1,000,000 quota / 500,000 (default QUOTA_PER_USD) = $2.0000.
    const rows = [
      makeQuotaRow({
        model_name: 'model-a',
        created_at: twoDaysAgo,
        quota: 300000,
      }),
      makeQuotaRow({
        model_name: 'model-a',
        created_at: twoDaysAgo,
        quota: 400000,
      }),
      makeQuotaRow({
        model_name: 'model-b',
        created_at: oneDayAgo,
        quota: 300000,
      }),
    ];
    wireDashboard({
      quota: { data: { success: true, data: rows } },
      status: EXPORT_ON_STATUS,
    });

    render(React.createElement(HFDashboard));

    // Panel total — proves the per-day aggregation actually summed the rows,
    // not just echoed a truthy fetch.
    await waitFor(() => screen.getByText('$2.0000'));

    // Model distribution — highest-consuming model first.
    const row0 = await waitFor(() => screen.getByTestId('model-dist-row-0'));
    expect(row0.textContent).toContain('model-a');
    expect(row0.textContent).toContain('$1.4000');
    const row1 = screen.getByTestId('model-dist-row-1');
    expect(row1.textContent).toContain('model-b');
    expect(row1.textContent).toContain('$0.6000');

    // The dense per-day chart actually drew a bar carrying real data, not
    // just an empty shell — data-nonzero is a rendered DOM attribute, not a
    // mock-call assertion.
    const chart = screen.getByTestId('usage-trend-chart');
    expect(
      chart.querySelectorAll('[data-nonzero="true"]').length,
    ).toBeGreaterThan(0);

    // The empty-state copy must NOT be showing alongside real data.
    expect(screen.queryByText('No usage recorded in this window.')).toBeNull();

    // The window label interpolates the real constant rather than a
    // hard-coded "29" that could silently drift from
    // DASHBOARD_TREND_WINDOW_SECONDS (index.jsx:70).
    expect(screen.getByText('last 29 days')).toBeTruthy();
  });

  it('treats the >30-day refusal (success:false, HTTP 200) as an empty state, not a toast', async () => {
    const { showError } = await import('../../../helpers');
    showError.mockClear();
    wireDashboard({
      quota: {
        data: { success: false, message: '时间跨度不能超过 1 个月' },
      },
      status: EXPORT_ON_STATUS,
    });

    render(React.createElement(HFDashboard));

    await waitFor(() => screen.getByText('No usage recorded in this window.'));
    expect(showError).not.toHaveBeenCalled();
    // The rest of the page must still be usable — the refusal degrades only
    // the trend panel, it does not blank the dashboard.
    expect(screen.getByText('total spend')).toBeTruthy();
  });

  it('renders nothing for the model distribution panel body when /api/data/self/ has no rows', async () => {
    wireDashboard({
      quota: { data: { success: true, data: [] } },
      status: EXPORT_ON_STATUS,
    });

    render(React.createElement(HFDashboard));

    await waitFor(() =>
      screen.getByText('No model consumption recorded in this window.'),
    );
    expect(screen.queryByTestId('model-dist-row-0')).toBeNull();
  });

  // With data export off, the aggregator never wrote quota_data
  // (repo/log.go:584, repo/usedata.go:18) — /api/data/self/ answering [] means
  // "we never collected this", not "no usage today". Printing the trend
  // panel's empty state next to a non-zero total-spend KPI would assert
  // something false about the account's data, so both panels must be absent
  // entirely — not degrade to their empty-state copy — matching the legacy
  // SiderBar gate on the same flag (SiderBar.jsx:79).
  it('renders neither panel — not even the empty state — when enable_data_export is false, despite real usage rows', async () => {
    const now = Math.floor(Date.now() / 1000);
    const rows = [
      makeQuotaRow({ model_name: 'model-a', created_at: now, quota: 500000 }),
    ];
    wireDashboard({
      quota: { data: { success: true, data: rows } },
      status: { data: { success: true, data: { enable_data_export: false } } },
    });

    render(React.createElement(HFDashboard));

    await waitFor(() => screen.getByText('total spend'));
    expect(screen.queryByTestId('usage-trend-chart')).toBeNull();
    expect(screen.queryByText('No usage recorded in this window.')).toBeNull();
    expect(screen.queryByTestId('model-dist-row-0')).toBeNull();
    expect(
      screen.queryByText('No model consumption recorded in this window.'),
    ).toBeNull();
  });

  // fetchExtras uses Promise.allSettled (index.jsx), not Promise.all, so a
  // rejection on one of the three calls must not blank the panels fed by the
  // other two: /api/data/self/ rejects while /api/status answers 200 with
  // FAQ content, and the FAQ panel must still render.
  it('still renders the FAQ panel when /api/data/self/ rejects', async () => {
    wireDashboard({
      status: {
        data: {
          success: true,
          data: {
            faq_enabled: true,
            faq: [{ question: 'Still up?', answer: 'Yes.' }],
          },
        },
      },
    });
    const wired = API.get.getMockImplementation();
    API.get.mockImplementation((url) => {
      if (String(url).includes('/api/data/self/')) {
        return Promise.reject(new Error('network unreachable'));
      }
      return wired(url);
    });

    render(React.createElement(HFDashboard));

    await waitFor(() => screen.getByTestId('dash-faq-panel'));
    expect(screen.getByText('Still up?')).toBeTruthy();
    // The rejected panel must degrade to its own empty state, not vanish the
    // page or leave quotaLoaded stuck false forever.
    expect(screen.getByText('total spend')).toBeTruthy();
  });

  // The trend chart's bars are keyed on the LOCAL calendar day
  // (index.jsx localDayKey), matching the label HfUsageTrendChart renders via
  // toLocaleDateString. Two rows exactly one hour apart, straddling local
  // midnight, must land in two distinct day buckets — with the old
  // UTC-keyed bucketing (Math.floor(ts / 86400) * 86400) this test's runtime
  // offset (a fixed non-UTC zone, see web/vitest / bun runtime) puts both
  // rows in the *same* UTC calendar day, merging them into one bar instead.
  it('buckets two rows straddling local midnight into two distinct day bars, not merged into one', async () => {
    // This guard only distinguishes local-day from UTC-day bucketing at a
    // non-zero UTC offset; at TZ=UTC the buggy code passes it. vitest.config.js
    // pins TZ for exactly this reason, so assert the pin rather than silently
    // becoming a no-op if someone removes it.
    expect(new Date().getTimezoneOffset()).not.toBe(0);

    const beforeMidnight = new Date();
    beforeMidnight.setDate(beforeMidnight.getDate() - 2);
    beforeMidnight.setHours(23, 30, 0, 0);
    const afterMidnight = new Date(beforeMidnight);
    // setHours(24, ...) rolls over to 00:30 local the next calendar day —
    // exactly one hour after beforeMidnight in absolute time.
    afterMidnight.setHours(24, 30, 0, 0);

    const rows = [
      makeQuotaRow({
        model_name: 'model-a',
        created_at: Math.floor(beforeMidnight.getTime() / 1000),
        quota: 111000,
      }),
      makeQuotaRow({
        model_name: 'model-a',
        created_at: Math.floor(afterMidnight.getTime() / 1000),
        quota: 222000,
      }),
    ];
    wireDashboard({
      quota: { data: { success: true, data: rows } },
      status: EXPORT_ON_STATUS,
    });

    render(React.createElement(HFDashboard));

    const chart = await waitFor(() => screen.getByTestId('usage-trend-chart'));
    // Two rows one hour apart, on two different local calendar days, must
    // produce two nonzero bars — a UTC-day bucket would merge them into one
    // in this runtime's timezone.
    expect(chart.querySelectorAll('[data-nonzero="true"]').length).toBe(2);
  });
});

describe('Dashboard page — /api/status panels (announcements / faq / api_info)', () => {
  it('shows each panel only when its own *_enabled flag is true AND it has content', async () => {
    wireDashboard({
      status: {
        data: {
          success: true,
          data: {
            // Off despite having content — must stay absent.
            announcements_enabled: false,
            announcements: [
              { content: 'hello', publishDate: '2026-09-01T00:00:00Z' },
            ],
            // On with content — must render.
            faq_enabled: true,
            faq: [{ question: 'How do I top up?', answer: 'Visit Billing.' }],
            // On but empty — must stay absent (flag alone is not enough).
            api_info_enabled: true,
            api_info: [],
          },
        },
      },
    });

    render(React.createElement(HFDashboard));

    await waitFor(() => screen.getByTestId('dash-faq-panel'));
    expect(screen.getByText('How do I top up?')).toBeTruthy();

    expect(screen.queryByTestId('dash-announcements-panel')).toBeNull();
    expect(screen.queryByTestId('dash-apiinfo-panel')).toBeNull();
  });

  it('parses a malformed (non-array, non-JSON) option as empty instead of crashing', async () => {
    wireDashboard({
      status: {
        data: {
          success: true,
          data: {
            faq_enabled: true,
            faq: 'not json {{{',
          },
        },
      },
    });

    render(React.createElement(HFDashboard));

    // Page still renders normally; the malformed FAQ option just yields no panel.
    await waitFor(() => screen.getByText('total spend'));
    expect(screen.queryByTestId('dash-faq-panel')).toBeNull();
  });
});

describe('Dashboard page — /api/uptime/status panel', () => {
  it('renders monitors when uptime_kuma_enabled is true and groups are non-empty', async () => {
    wireDashboard({
      status: {
        data: { success: true, data: { uptime_kuma_enabled: true } },
      },
      uptime: {
        data: {
          success: true,
          data: [
            {
              categoryName: 'core',
              monitors: [{ name: 'relay-api', uptime: 0.999, status: 1 }],
            },
          ],
        },
      },
    });

    render(React.createElement(HFDashboard));

    await waitFor(() => screen.getByTestId('dash-uptime-panel'));
    expect(screen.getByText('relay-api')).toBeTruthy();
  });

  it('stays absent when uptime_kuma_enabled is false even though groups are configured', async () => {
    // /api/uptime/status has no flag of its own (uptime_kuma.go:131 returns
    // data regardless) — the gate has to come from /api/status's flag.
    wireDashboard({
      status: {
        data: { success: true, data: { uptime_kuma_enabled: false } },
      },
      uptime: {
        data: {
          success: true,
          data: [
            {
              categoryName: 'core',
              monitors: [{ name: 'relay-api', uptime: 0.999, status: 1 }],
            },
          ],
        },
      },
    });

    render(React.createElement(HFDashboard));

    await waitFor(() => screen.getByText('total spend'));
    expect(screen.queryByTestId('dash-uptime-panel')).toBeNull();
  });

  it('stays absent when the group list is empty (default GetUptimeKumaStatus response)', async () => {
    wireDashboard({
      status: {
        data: { success: true, data: { uptime_kuma_enabled: true } },
      },
      uptime: { data: { success: true, data: [] } },
    });

    render(React.createElement(HFDashboard));

    await waitFor(() => screen.getByText('total spend'));
    expect(screen.queryByTestId('dash-uptime-panel')).toBeNull();
  });
});

// ─── L7 (cycle 13): honest load-failure reporting ─────────────────────────
//
// fetchData() fetches /user/me and /logs. Before this lane, ANY failure of
// either call — a 502, a dropped connection, a 200 {success:false} refusal,
// a 403, a 401 — left `me`/`logs` at their initial empty values, which the
// KPI strip could not tell apart from "fetched, and this account is
// genuinely idle": it printed "no traffic in last 5 min" for a dashboard
// that had not, in fact, checked. Each of the five failure shapes below
// must instead show the retry banner and must NOT print the
// confirmed-empty copy.
describe('Dashboard page — honest load failure (meStatus/logsStatus)', () => {
  const FAILURE_MODES = [
    [
      '502 from the server',
      () => Promise.reject({ response: { status: 502 } }),
    ],
    [
      'a rejected/dropped connection',
      () => Promise.reject(new Error('network down')),
    ],
    [
      '200 {success:false}',
      () => Promise.resolve({ data: { success: false, message: 'nope' } }),
    ],
    ['403', () => Promise.reject({ response: { status: 403 } })],
    ['401', () => Promise.reject({ response: { status: 401 } })],
  ];

  // Both fetchData() calls fail the same way; the other three (fetchExtras)
  // succeed emptily so only the KPI-strip failure is under test.
  const wireFailingKpiStrip = (makeOutcome) => {
    API.get.mockImplementation((url) => {
      const u = String(url);
      if (u.includes('/user/me') || u.includes('/logs')) return makeOutcome();
      return Promise.resolve({ data: { success: true, data: {} } });
    });
  };

  it.each(FAILURE_MODES)(
    'shows the retry banner and hides "confirmed empty" copy for %s',
    async (_label, makeOutcome) => {
      wireFailingKpiStrip(makeOutcome);

      render(React.createElement(HFDashboard));

      await waitFor(() =>
        expect(screen.getByTestId('dashboard-load-error')).toBeTruthy(),
      );
      expect(screen.getByTestId('dashboard-retry-btn')).toBeTruthy();
      // "confirmed empty" claims a check happened and found nothing — false
      // when the fetch itself never answered.
      expect(screen.queryByText('no traffic in last 5 min')).toBeNull();
      expect(screen.queryByText('No recent requests found.')).toBeNull();
      expect(
        screen.queryByText(
          'once a relay call is consumed, the model breakdown lands here.',
        ),
      ).toBeNull();
      // No formatted-zero KPI either — "$0.00" is as much a confirmed-empty
      // claim as the prose above.
      expect(screen.queryByText('$0.00')).toBeNull();
    },
  );

  it('a successful fetch with real rows shows real numbers, not the retry banner', async () => {
    const now = Math.floor(Date.now() / 1000);
    API.get.mockImplementation((url) => {
      const u = String(url);
      if (u.includes('/user/me')) {
        return Promise.resolve({
          data: { success: true, data: makeMe({ request_count: 12 }) },
        });
      }
      if (u.includes('/logs')) {
        return Promise.resolve({
          data: {
            success: true,
            data: { logs: [makeLog({ created_at: now - 5 })], total: 1 },
          },
        });
      }
      return Promise.resolve({ data: { success: true, data: {} } });
    });

    render(React.createElement(HFDashboard));

    await waitFor(() => screen.getByText('12'));
    expect(screen.queryByTestId('dashboard-load-error')).toBeNull();
  });

  it('the retry button re-issues both fetchData calls', async () => {
    let kpiCallCount = 0;
    API.get.mockImplementation((url) => {
      const u = String(url);
      if (u.includes('/user/me') || u.includes('/logs')) {
        kpiCallCount += 1;
        return Promise.reject({ response: { status: 500 } });
      }
      return Promise.resolve({ data: { success: true, data: {} } });
    });

    render(React.createElement(HFDashboard));
    await waitFor(() => screen.getByTestId('dashboard-retry-btn'));
    const callsBeforeRetry = kpiCallCount;

    const { fireEvent } = await import('@testing-library/react');
    fireEvent.click(screen.getByTestId('dashboard-retry-btn'));

    await waitFor(() => expect(kpiCallCount).toBeGreaterThan(callsBeforeRetry));
  });
});
