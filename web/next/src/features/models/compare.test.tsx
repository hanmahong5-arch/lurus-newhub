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

import { ModelsPage } from './index';
import { WIRE_CHAT } from './lib/wire';

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
      <ModelsPage />
    </QueryClientProvider>,
  );
}

type Routes = Record<string, unknown>;

function serve(routes: Routes) {
  get.mockImplementation(async (path: string) => {
    const key = Object.keys(routes).find((k) => path.startsWith(k));
    if (!key) throw new ApiError('not found', { status: 404 });
    const v = routes[key];
    if (v instanceof Error) throw v;
    return v;
  });
}

// quota_per_unit 500000 -> $2 per 1M tokens per unit of model_ratio.
const PRICING = [
  {
    model_name: 'cheap-m',
    vendor: 'VendorA',
    model_ratio: 0.5,
    completion_ratio: 2,
    supported_endpoint_types: [WIRE_CHAT],
    description: 'The cheap one',
  },
  {
    model_name: 'pricey-m',
    vendor: 'VendorB',
    model_ratio: 2,
    completion_ratio: 3,
    cache_ratio: 0.5,
  },
  {
    model_name: 'call-m',
    vendor: 'VendorA',
    quota_type: 1,
    model_price: 0.05,
  },
  { model_name: 'unpriced-m', vendor: 'VendorB', price_configured: false },
  {
    model_name: 'search-m',
    vendor: 'VendorB',
    usage_unit: 'search_unit',
    search_unit_price: 0.01,
  },
];

const base: Routes = {
  '/user/me': { group: 'default', role: 10 },
  '/models/routable': {
    items: [{ id: 'cheap-m', owned_by: 'VendorA' }],
  },
  '/pricing': { pricing: PRICING, group_ratio: { default: 1 } },
  '/models?': { items: [], total: 0 },
  '/analytics/rankings': {
    rows: [{ name: 'cheap-m', total_tokens: 2500, requests: 4 }],
  },
  '/models/performance': {
    items: [
      {
        model_name: 'cheap-m',
        p50_latency_ms: 850,
        p95_latency_ms: 1500,
        error_rate: 0.02,
        enough_samples: true,
      },
      {
        model_name: 'pricey-m',
        p50_latency_ms: 90,
        p95_latency_ms: 90,
        error_rate: 0.5,
        enough_samples: false,
      },
    ],
  },
};

const toggle = (id: string) => screen.getByTestId(`model-compare-${id}`);

async function pick(
  user: ReturnType<typeof userEvent.setup>,
  ...ids: string[]
) {
  for (const id of ids) await user.click(toggle(id));
}

async function openCompare(
  user: ReturnType<typeof userEvent.setup>,
  ...ids: string[]
) {
  await pick(user, ...ids);
  await user.click(screen.getByTestId('compare-open'));
  return screen.findByTestId('compare-table');
}

const row = (key: string) => screen.getByTestId(`compare-row-${key}`);
const cells = (key: string) => within(row(key)).getAllByRole('cell');

describe('Models compare view', () => {
  beforeEach(() => {
    get.mockReset();
    apiGet.mockReset();
    apiGet.mockResolvedValue({ quota_per_unit: 500000 });
    window.localStorage.clear();
  });

  it('shows no bar until a model is picked, and offers compare from two', async () => {
    serve(base);
    renderPage();
    await screen.findByTestId('model-card-cheap-m');
    expect(screen.queryByTestId('compare-bar')).not.toBeInTheDocument();
    const user = userEvent.setup();

    await user.click(toggle('cheap-m'));
    const bar = screen.getByTestId('compare-bar');
    expect(bar).toHaveTextContent('1 selected');
    expect(bar).toHaveTextContent('up to 4');
    // One model is nothing to compare against.
    expect(screen.getByTestId('compare-open')).toBeDisabled();

    await user.click(toggle('pricey-m'));
    expect(bar).toHaveTextContent('2 selected');
    expect(screen.getByTestId('compare-open')).toBeEnabled();
    // Picking never opens the detail drawer.
    expect(screen.queryByTestId('model-drawer')).not.toBeInTheDocument();
    expect(toggle('cheap-m')).toHaveAttribute('aria-pressed', 'true');
  });

  it('puts the models side by side and marks the cheapest input and output', async () => {
    serve(base);
    renderPage();
    await screen.findByTestId('model-card-cheap-m');
    const user = userEvent.setup();
    const table = await openCompare(user, 'cheap-m', 'pricey-m');

    expect(
      within(table).getByTestId('compare-details-cheap-m'),
    ).toBeInTheDocument();
    expect(table.querySelectorAll('thead th')).toHaveLength(3);

    // cheap: $1 in / $2 out per 1M; pricey: $4 in / $12 out.
    const input = cells('input');
    expect(input[0]).toHaveTextContent('$1');
    expect(input[0]).toHaveTextContent('Cheapest');
    expect(input[1]).toHaveTextContent('$4');
    expect(input[1]).not.toHaveTextContent('Cheapest');
    const output = cells('output');
    expect(output[0]).toHaveTextContent('$2');
    expect(output[0]).toHaveTextContent('Cheapest');
    expect(output[1]).toHaveTextContent('$12');
    expect(output[1]).not.toHaveTextContent('Cheapest');

    // cache read: only pricey-m configures one ($4 x 0.5).
    expect(cells('cache')[0]).toHaveTextContent('--');
    expect(cells('cache')[1]).toHaveTextContent('$2');
    expect(cells('vendor')[0]).toHaveTextContent('VendorA');
    expect(cells('access')[0]).toHaveTextContent('Callable');
    expect(cells('access')[1]).toHaveTextContent('Not in your groups');
    expect(cells('tokens')[0]).toHaveTextContent('2.5K');
    expect(cells('desc')[0]).toHaveTextContent('The cheap one');
    expect(cells('desc')[1]).toHaveTextContent('--');
  });

  it('follows the per-1K price unit', async () => {
    serve(base);
    renderPage();
    await screen.findByTestId('model-card-cheap-m');
    const user = userEvent.setup();
    await user.click(screen.getByTestId('models-unit-K'));
    await openCompare(user, 'cheap-m', 'pricey-m');
    expect(cells('input')[0]).toHaveTextContent('$0.001');
    expect(cells('input')[0]).toHaveTextContent('Cheapest');
    expect(row('input')).toHaveTextContent('Input / 1K');
  });

  it('marks nothing without a contest and never ranks free-looking or differently billed models', async () => {
    serve(base);
    renderPage();
    await screen.findByTestId('model-card-cheap-m');
    const user = userEvent.setup();
    // Per-call, unpriced and search-unit models do not compete with token prices.
    await openCompare(user, 'cheap-m', 'call-m', 'unpriced-m', 'search-m');
    expect(
      within(row('input')).queryByText('Cheapest'),
    ).not.toBeInTheDocument();
    expect(
      within(row('output')).queryByText('Cheapest'),
    ).not.toBeInTheDocument();

    const input = cells('input');
    expect(input[1]).toHaveTextContent('$0.05');
    expect(input[1]).toHaveTextContent('per call');
    // Unpriced is "Price not configured", never $0 and never the cheapest.
    expect(input[2]).toHaveTextContent('Price not configured');
    expect(input[2].textContent).not.toContain('$');
    expect(input[3]).toHaveTextContent('$0.01');
    expect(input[3]).toHaveTextContent('search unit');
    // Output is not a per-token figure for per-call or search-unit models.
    expect(cells('output')[1]).toHaveTextContent('--');
    expect(cells('output')[3]).toHaveTextContent('--');
  });

  it('says why a thin or missing measurement is not shown instead of guessing', async () => {
    serve(base);
    renderPage();
    await screen.findByTestId('model-card-cheap-m');
    const user = userEvent.setup();
    await openCompare(user, 'cheap-m', 'pricey-m', 'call-m');
    expect(cells('success')[0]).toHaveTextContent('98.00%');
    expect(cells('p50')[0]).toHaveTextContent('850ms');
    expect(cells('p95')[0]).toHaveTextContent('1.50s');
    // 90 ms from too few samples is not presented as a measurement.
    expect(cells('success')[1]).toHaveTextContent(
      'Too little traffic to measure',
    );
    expect(cells('p50')[1].textContent).not.toContain('90');
    expect(cells('p50')[1]).toHaveTextContent('--');
    expect(cells('success')[2]).toHaveTextContent('No traffic yet');
    expect(row('p50')).toHaveTextContent('last 24 hours');
  });

  it('does not claim 0 tokens for a viewer who cannot read usage', async () => {
    serve({ ...base, '/user/me': { group: 'default', role: 1 } });
    renderPage();
    await screen.findByTestId('model-card-cheap-m');
    const user = userEvent.setup();
    await openCompare(user, 'cheap-m', 'pricey-m');
    expect(cells('tokens')[0]).toHaveTextContent('--');
    expect(cells('tokens')[0].textContent).not.toContain('0');
  });

  it('holds at most four models', async () => {
    serve(base);
    renderPage();
    await screen.findByTestId('model-card-cheap-m');
    const user = userEvent.setup();
    await pick(user, 'cheap-m', 'pricey-m', 'call-m', 'unpriced-m');
    expect(screen.getByTestId('compare-bar')).toHaveTextContent('4 selected');
    const fifth = toggle('search-m');
    expect(fifth).toHaveAttribute('aria-pressed', 'false');
    expect(fifth).toBeDisabled();
    await user.click(fifth);
    expect(screen.getByTestId('compare-bar')).toHaveTextContent('4 selected');

    // Dropping one frees the slot.
    await user.click(toggle('call-m'));
    expect(toggle('search-m')).toBeEnabled();
    await user.click(toggle('search-m'));
    expect(screen.getByTestId('compare-bar')).toHaveTextContent('4 selected');
    expect(toggle('search-m')).toHaveAttribute('aria-pressed', 'true');
  });

  it('removes a model from the sheet, and closes the sheet when the last one goes', async () => {
    serve(base);
    renderPage();
    await screen.findByTestId('model-card-cheap-m');
    const user = userEvent.setup();
    const table = await openCompare(user, 'cheap-m', 'pricey-m');

    await user.click(within(table).getByTestId('compare-remove-pricey-m'));
    expect(table.querySelectorAll('thead th')).toHaveLength(2);
    // The lone remaining model has no rival, so no cheapest mark.
    expect(
      within(row('input')).queryByText('Cheapest'),
    ).not.toBeInTheDocument();
    expect(screen.getByTestId('compare-bar')).toHaveTextContent('1 selected');

    await user.click(within(table).getByTestId('compare-remove-cheap-m'));
    await waitFor(() =>
      expect(screen.queryByTestId('compare-drawer')).not.toBeInTheDocument(),
    );
    expect(screen.queryByTestId('compare-bar')).not.toBeInTheDocument();
    expect(toggle('cheap-m')).toHaveAttribute('aria-pressed', 'false');
  });

  it('clears the whole selection from the bar', async () => {
    serve(base);
    renderPage();
    await screen.findByTestId('model-card-cheap-m');
    const user = userEvent.setup();
    await pick(user, 'cheap-m', 'pricey-m');
    await user.click(screen.getByTestId('compare-clear'));
    expect(screen.queryByTestId('compare-bar')).not.toBeInTheDocument();
    expect(toggle('pricey-m')).toHaveAttribute('aria-pressed', 'false');
  });

  it('opens the detail drawer from the sheet, and Escape closes only the one on top', async () => {
    serve(base);
    renderPage();
    await screen.findByTestId('model-card-cheap-m');
    const user = userEvent.setup();
    const table = await openCompare(user, 'cheap-m', 'pricey-m');
    const sheet = screen.getByTestId('compare-drawer');
    expect(sheet).toHaveAttribute('role', 'dialog');
    expect(screen.getByRole('dialog', { name: 'Compare models' })).toBe(sheet);

    await user.click(within(table).getByTestId('compare-details-cheap-m'));
    expect(await screen.findByTestId('model-drawer')).toBeInTheDocument();

    // Focus lands on the drawer's copy button, whose tooltip eats the first
    // Escape; close that first so the next one is the dialog's.
    screen.getByRole('button', { name: 'Close' }).focus();
    await user.keyboard('{Escape}');
    await waitFor(() =>
      expect(screen.queryByTestId('model-drawer')).not.toBeInTheDocument(),
    );
    expect(screen.getByTestId('compare-drawer')).toBeInTheDocument();
    expect(screen.getByTestId('compare-table')).toBeInTheDocument();
  });

  it('can be picked from the table view without opening the detail drawer', async () => {
    serve(base);
    renderPage();
    await screen.findByTestId('model-card-cheap-m');
    const user = userEvent.setup();
    await user.click(toggle('cheap-m'));
    await user.click(screen.getByRole('button', { name: 'Table view' }));
    await screen.findByTestId('model-row-cheap-m');
    // The pick survives the view switch.
    expect(toggle('cheap-m')).toHaveAttribute('aria-pressed', 'true');
    await user.click(toggle('pricey-m'));
    expect(screen.queryByTestId('model-drawer')).not.toBeInTheDocument();
    expect(screen.getByTestId('compare-bar')).toHaveTextContent('2 selected');
    await user.click(screen.getByTestId('compare-open'));
    expect(await screen.findByTestId('compare-table')).toBeInTheDocument();
  });

  it('keeps picks across filtering', async () => {
    serve(base);
    renderPage();
    await screen.findByTestId('model-card-cheap-m');
    const user = userEvent.setup();
    await pick(user, 'cheap-m', 'pricey-m');
    // Narrowing the list to one vendor hides the card, not the pick.
    await user.click(screen.getByTestId('vendor-facet-VendorA'));
    expect(screen.queryByTestId('model-card-pricey-m')).not.toBeInTheDocument();
    expect(screen.getByTestId('compare-bar')).toHaveTextContent('2 selected');
    await user.click(screen.getByTestId('compare-open'));
    const table = await screen.findByTestId('compare-table');
    expect(
      within(table).getByTestId('compare-details-pricey-m'),
    ).toBeInTheDocument();
  });
});
