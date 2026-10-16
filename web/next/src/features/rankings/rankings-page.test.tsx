/*
Copyright (C) 2023-2026 QuantumNous

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
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { beforeEach, describe, expect, it, vi } from 'vitest';

import { ApiError } from '@/lib/api-error';

import { RankingsPage } from './index';

const { get, apiGet } = vi.hoisted(() => ({ get: vi.fn(), apiGet: vi.fn() }));
vi.mock('@/lib/api', async (orig) => {
  const mod = await orig<typeof import('@/lib/api')>();
  return {
    ...mod,
    api: { ...mod.api, get: apiGet },
    tenantApi: { ...mod.tenantApi, get },
  };
});


function renderPage() {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  return render(
    <QueryClientProvider client={client}>
      <RankingsPage />
    </QueryClientProvider>,
  );
}

const row = (over: Record<string, unknown>) => ({
  name: 'model-a',
  rank: 1,
  rank_delta: 0,
  is_new: false,
  requests: 1200,
  total_tokens: 2_500_000,
  quota: 500000,
  token_share_pct: 62.5,
  quota_share_pct: 60,
  requests_growth_pct: 12.5,
  ...over,
});

const NOW = 1_700_000_000 - (1_700_000_000 % 3600);

function snapshot(by: string, hours: number) {
  if (by === 'group') {
    return {
      by,
      hours,
      window: { start: NOW - 3600, end: NOW },
      cached_at: NOW,
      total_tokens: 100,
      total_quota: 100,
      rows: [row({ name: 'vip', token_share_pct: 80, total_tokens: 80 })],
      series: [
        { t: NOW - 3600, name: 'vip', tokens: 80, requests: 1, quota: 1 },
      ],
    };
  }
  return {
    by,
    hours,
    window: { start: NOW - 3600, end: NOW },
    cached_at: NOW,
    total_tokens: 4_000_000,
    total_quota: 800000,
    rows: [
      row({ name: 'model-a', rank: 1, rank_delta: 2 }),
      row({
        name: 'model-b',
        rank: 2,
        rank_delta: -1,
        total_tokens: 1_000_000,
        token_share_pct: 25,
        requests_growth_pct: -30,
      }),
      row({
        name: 'model-c',
        rank: 3,
        is_new: true,
        total_tokens: 500_000,
        token_share_pct: 12.5,
        requests_growth_pct: null,
      }),
    ],
    series: [
      { t: NOW - 3600, name: 'model-a', tokens: 1_000_000, requests: 5, quota: 1 },
      { t: NOW, name: 'model-b', tokens: 400_000, requests: 2, quota: 1 },
    ],
  };
}

const TENANT_USER = { id: 1, username: 'u', role: 1, tenant_role: 'admin' };
const ROOT_USER = { id: 2, username: 'root', role: 100, tenant_role: 'admin' };
const ADMIN_RANKINGS = '/api/v2/admin/analytics/rankings';
const ADMIN_TENANTS = '/api/v2/admin/tenants';

type Cfg = { params?: { by: string; hours: number; tenant_id?: string } };

/** Tenant-scoped reads (`tenantApi.get`): the session user plus the board. */
function serve(user: Record<string, unknown> = TENANT_USER) {
  get.mockImplementation(async (path: string, cfg?: Cfg) => {
    if (path === '/user/me') return user;
    return snapshot(cfg?.params?.by ?? 'model', cfg?.params?.hours ?? 168);
  });
}

/** Plain reads (`api.get`): the money config, the cross-tenant board, tenants. */
function serveAdmin(opts: { board?: (cfg?: Cfg) => unknown } = {}) {
  apiGet.mockImplementation(async (url: string, cfg?: Cfg) => {
    if (url === ADMIN_RANKINGS) {
      return opts.board
        ? opts.board(cfg)
        : snapshot(cfg?.params?.by ?? 'model', cfg?.params?.hours ?? 168);
    }
    if (url === ADMIN_TENANTS) {
      return {
        tenants: [
          { id: 't-1', name: 'Acme', slug: 'acme' },
          { id: 't-2', slug: 'globex' },
        ],
        total: 2,
      };
    }
    return { quota_per_unit: 500000 };
  });
}

const adminBoardCalls = () =>
  apiGet.mock.calls.filter(([url]) => url === ADMIN_RANKINGS);

describe('RankingsPage', () => {
  beforeEach(() => {
    get.mockReset();
    apiGet.mockReset();
    apiGet.mockResolvedValue({ quota_per_unit: 500000 });
  });

  it('renders the ranked table with rank movement, share, spend and growth', async () => {
    serve();
    renderPage();
    const table = await screen.findByTestId('rankings-model-table');
    const a = within(table).getByTestId('rank-row-model-a');
    expect(a).toHaveTextContent('2.50M');
    expect(a).toHaveTextContent('62.5%');
    expect(a).toHaveTextContent('↑12.5%');
    expect(within(table).getByTestId('rank-delta-model-a')).toHaveTextContent(
      '2',
    );
    expect(within(table).getByTestId('rank-row-model-b')).toHaveTextContent(
      '↓30.0%',
    );
    // newcomer: a badge, and no growth figure to compare with
    const c = within(table).getByTestId('rank-row-model-c');
    expect(within(c).getByTestId('rank-new-model-c')).toBeInTheDocument();
    expect(c).not.toHaveTextContent('%↑');
    await waitFor(() => expect(a).toHaveTextContent('$1'));
    expect(screen.getByTestId('rankings-total-tokens')).toHaveTextContent(
      '4.00M',
    );
  });

  it('asks the endpoint for the hours of the chosen period only', async () => {
    serve();
    renderPage();
    await screen.findByTestId('rankings-model-table');
    const hoursAsked = () =>
      get.mock.calls
        .filter(([, cfg]) => cfg?.params?.by === 'model')
        .map(([, cfg]) => cfg.params.hours);
    expect(hoursAsked()).toEqual([168]);
    const user = userEvent.setup();
    await user.click(screen.getByTestId('rankings-period-today'));
    await waitFor(() => expect(hoursAsked()).toContain(24));
    await user.click(screen.getByTestId('rankings-period-month'));
    await waitFor(() => expect(hoursAsked()).toContain(720));
    expect(screen.queryByText('Year')).not.toBeInTheDocument();
  });

  it('lists climbers, fallers and newcomers from the rank deltas', async () => {
    serve();
    renderPage();
    await screen.findByTestId('rankings-pulse');
    expect(within(screen.getByTestId('rankings-up')).getByText('model-a'))
      .toBeInTheDocument();
    expect(within(screen.getByTestId('rankings-down')).getByText('model-b'))
      .toBeInTheDocument();
    expect(within(screen.getByTestId('rankings-new')).getByText('model-c'))
      .toBeInTheDocument();
  });

  it('switches the market-share dimension and never offers vendor', async () => {
    serve();
    renderPage();
    await screen.findByTestId('rankings-share-table');
    expect(screen.queryByRole('tab', { name: 'Vendor' })).not.toBeInTheDocument();
    await userEvent.setup().click(screen.getByTestId('rankings-dim-product'));
    await waitFor(() =>
      expect(
        get.mock.calls.some(([, cfg]) => cfg?.params?.by === 'product'),
      ).toBe(true),
    );
    expect(
      get.mock.calls.some(([, cfg]) => cfg?.params?.by === 'vendor'),
    ).toBe(false);
  });

  it('says so when there was no traffic instead of drawing empty charts', async () => {
    get.mockImplementation(async (p: string, cfg?: { params?: { by: string } }) => ({
      ...(p === '/user/me' ? TENANT_USER : snapshot(cfg?.params?.by ?? 'model', 168)),
      rows: [],
      series: [],
      total_tokens: 0,
    }));
    renderPage();
    expect(
      await screen.findByText('No model traffic in this period'),
    ).toBeInTheDocument();
    expect(screen.getByTestId('rankings-chart-empty')).toBeInTheDocument();
    expect(screen.getByText('No notable climbers right now')).toBeInTheDocument();
  });

  it('shows the server refusal, not an empty board, and retries', async () => {
    let refused = true;
    get.mockImplementation(async (path: string, cfg?: Cfg) => {
      if (path === '/user/me') return TENANT_USER;
      if (refused) {
        refused = false;
        throw new ApiError('Admin role required', { status: 403 });
      }
      return snapshot(cfg?.params?.by ?? 'model', cfg?.params?.hours ?? 168);
    });
    renderPage();
    const err = await screen.findByTestId('rankings-error');
    expect(err).toHaveTextContent('Failed to load rankings');
    expect(err).toHaveTextContent('Admin role required');
    expect(screen.queryByTestId('rankings-model-table')).not.toBeInTheDocument();
    await userEvent.setup().click(within(err).getByRole('button'));
    expect(await screen.findByTestId('rankings-model-table')).toBeInTheDocument();
  });

  it('offers no scope switch to a non-root user and never calls the admin route', async () => {
    serve();
    serveAdmin();
    renderPage();
    await screen.findByTestId('rankings-model-table');
    expect(screen.queryByTestId('rankings-scope')).not.toBeInTheDocument();
    expect(screen.queryByText('All tenants')).not.toBeInTheDocument();
    expect(adminBoardCalls()).toHaveLength(0);
    expect(apiGet.mock.calls.some(([url]) => url === ADMIN_TENANTS)).toBe(false);
  });

  it('starts root on its own tenant, then reads the cross-tenant board on switch', async () => {
    serve(ROOT_USER);
    serveAdmin({
      board: (cfg) => ({
        ...snapshot(cfg?.params?.by ?? 'model', cfg?.params?.hours ?? 168),
        rows: [row({ name: 'model-x', total_tokens: 9_000_000 })],
      }),
    });
    renderPage();
    expect(await screen.findByTestId('rankings-scope')).toBeInTheDocument();
    await screen.findByTestId('rankings-model-table');
    // own tenant first: the tenant route answered, the admin route is untouched
    expect(adminBoardCalls()).toHaveLength(0);
    const models = () => screen.getByTestId('rankings-model-table');
    expect(within(models()).getByTestId('rank-row-model-a')).toBeInTheDocument();

    await userEvent.setup().click(screen.getByTestId('rankings-scope-all'));
    const x = await within(
      await screen.findByTestId('rankings-model-table'),
    ).findByTestId('rank-row-model-x');
    expect(x).toHaveTextContent('9.00M');
    expect(within(models()).queryByTestId('rank-row-model-a')).not.toBeInTheDocument();
    const first = adminBoardCalls()[0][1] as Cfg;
    expect(first.params).toEqual({ by: 'model', hours: 168 });
    expect(first.params).not.toHaveProperty('tenant_id');
    // the same period mapping applies on the admin route
    await userEvent.setup().click(screen.getByTestId('rankings-period-month'));
    await waitFor(() =>
      expect(adminBoardCalls().map(([, c]) => (c as Cfg).params?.hours)).toContain(720),
    );
  });

  it('filters the cross-tenant board to one tenant by tenant_id', async () => {
    serve(ROOT_USER);
    serveAdmin();
    renderPage();
    const user = userEvent.setup();
    await user.click(await screen.findByTestId('rankings-scope-all'));
    const filter = await screen.findByTestId('rankings-scope-tenant-filter');
    await waitFor(() =>
      expect(within(filter).getByRole('option', { name: 'Acme' })).toBeInTheDocument(),
    );
    // a tenant without a display name falls back to its slug
    expect(within(filter).getByRole('option', { name: 'globex' })).toBeInTheDocument();
    await user.selectOptions(filter, 't-1');
    await waitFor(() =>
      expect(
        adminBoardCalls().some(([, c]) => (c as Cfg).params?.tenant_id === 't-1'),
      ).toBe(true),
    );
  });

  it('shows a failed cross-tenant read as an error, not an empty board', async () => {
    serve(ROOT_USER);
    serveAdmin({
      board: () => {
        throw new ApiError('Failed to aggregate rankings', { status: 500 });
      },
    });
    renderPage();
    await screen.findByTestId('rankings-model-table');
    await userEvent.setup().click(screen.getByTestId('rankings-scope-all'));
    const err = await screen.findByTestId('rankings-error');
    expect(err).toHaveTextContent('Failed to aggregate rankings');
    expect(screen.queryByTestId('rankings-model-table')).not.toBeInTheDocument();
  });
});
