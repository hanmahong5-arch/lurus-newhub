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

const healthy: Routes = {
  '/user/me': { group: 'vip', role: 10 },
  '/models/routable': {
    items: [
      {
        id: 'model-a',
        owned_by: 'VendorA',
        supported_endpoint_types: [WIRE_CHAT],
      },
    ],
  },
  '/pricing': {
    pricing: [
      {
        model_name: 'model-a',
        vendor: 'VendorA',
        model_ratio: 1,
        completion_ratio: 3,
        tags: 'fast,chat',
        enable_groups: ['vip', 'default'],
      },
      {
        model_name: 'free-z',
        vendor: 'VendorB',
        model_ratio: 0,
        enable_groups: ['default'],
      },
    ],
    group_ratio: { vip: 0.5, default: 1 },
  },
  '/models?': { items: [], total: 0 },
  '/analytics/rankings': {
    rows: [{ name: 'model-a', total_tokens: 2500, requests: 4 }],
  },
  '/models/performance': { items: [] },
};

const measured = {
  model_name: 'model-a',
  p50_latency_ms: 850,
  p95_latency_ms: 1500,
  error_rate: 0.02,
  enough_samples: true,
};

describe('ModelsPage', () => {
  beforeEach(() => {
    get.mockReset();
    apiGet.mockReset();
    apiGet.mockResolvedValue({ quota_per_unit: 500000 });
    window.localStorage.clear();
  });

  it('shows prices after the group multiplier and "Price not configured" instead of $0', async () => {
    serve(healthy);
    renderPage();
    const card = await screen.findByTestId('model-card-model-a');
    // ratio 1 -> $2/M, group vip 0.5 -> $1/M in, completion 3 -> $3/M out
    expect(within(card).getByText('$1')).toBeInTheDocument();
    expect(within(card).getByText('$3')).toBeInTheDocument();
    expect(within(card).getByText('2.5K tokens · 7d')).toBeInTheDocument();

    const free = screen.getByTestId('model-card-free-z');
    expect(within(free).getAllByText('Price not configured').length).toBe(2);
    expect(free.textContent).not.toContain('$0');
    expect(within(free).queryByText('Free')).not.toBeInTheDocument();
  });

  it('labels a price_configured=false model unpriced even with a ratio on it', async () => {
    serve({
      ...healthy,
      '/pricing': {
        pricing: [
          {
            model_name: 'model-a',
            vendor: 'VendorA',
            model_ratio: 37.5,
            price_configured: false,
          },
        ],
        group_ratio: { vip: 1, default: 1 },
      },
    });
    renderPage();
    const card = await screen.findByTestId('model-card-model-a');
    expect(
      within(card).getAllByText('Price not configured').length,
    ).toBeGreaterThan(0);
    expect(within(card).queryByText('Input')).not.toBeInTheDocument();
    expect(card.textContent).not.toContain('$');
  });

  it('filters by vendor and switches to the table view', async () => {
    serve(healthy);
    renderPage();
    await screen.findByTestId('model-card-model-a');
    const user = userEvent.setup();
    await user.click(screen.getByTestId('vendor-facet-VendorB'));
    expect(screen.queryByTestId('model-card-model-a')).not.toBeInTheDocument();
    expect(screen.getByTestId('model-card-free-z')).toBeInTheDocument();
    await user.click(screen.getByRole('button', { name: 'Table view' }));
    expect(screen.getByTestId('model-row-free-z')).toBeInTheDocument();
  });

  it('searches names, vendors and tags, and resets from the empty state', async () => {
    serve(healthy);
    renderPage();
    await screen.findByTestId('model-card-model-a');
    const user = userEvent.setup();
    await user.type(screen.getByTestId('models-search'), 'free');
    expect(screen.queryByTestId('model-card-model-a')).not.toBeInTheDocument();
    expect(screen.getByTestId('model-card-free-z')).toBeInTheDocument();
    expect(screen.getByTestId('models-count')).toHaveTextContent('1 of 2');

    await user.clear(screen.getByTestId('models-search'));
    await user.type(screen.getByTestId('models-search'), 'fast');
    expect(screen.getByTestId('model-card-model-a')).toBeInTheDocument();
    expect(screen.queryByTestId('model-card-free-z')).not.toBeInTheDocument();

    await user.clear(screen.getByTestId('models-search'));
    await user.type(screen.getByTestId('models-search'), 'nothing-matches');
    expect(await screen.findByTestId('models-empty')).toBeInTheDocument();
    await user.click(screen.getByRole('button', { name: 'Clear all filters' }));
    expect(screen.getByTestId('model-card-model-a')).toBeInTheDocument();
    expect(screen.getByTestId('model-card-free-z')).toBeInTheDocument();
  });

  it('filters by billing kind so unpriced models can be found', async () => {
    serve(healthy);
    renderPage();
    await screen.findByTestId('model-card-model-a');
    const user = userEvent.setup();
    await user.click(screen.getByTestId('price-facet-unpriced'));
    expect(screen.queryByTestId('model-card-model-a')).not.toBeInTheDocument();
    expect(screen.getByTestId('model-card-free-z')).toBeInTheDocument();
    await user.click(screen.getByTestId('models-reset'));
    expect(screen.getByTestId('model-card-model-a')).toBeInTheDocument();
  });

  it('reprices under another group and hides models that group cannot call', async () => {
    serve(healthy);
    renderPage();
    const card = await screen.findByTestId('model-card-model-a');
    expect(within(card).getByText('$1')).toBeInTheDocument();
    const user = userEvent.setup();
    await user.click(screen.getByTestId('group-facet-default'));
    // default multiplier 1 -> $2/M in, $6/M out
    expect(
      within(screen.getByTestId('model-card-model-a')).getByText('$2'),
    ).toBeInTheDocument();
    expect(screen.getByTestId('model-card-free-z')).toBeInTheDocument();
    await user.click(screen.getByTestId('group-facet-vip'));
    expect(screen.queryByTestId('model-card-free-z')).not.toBeInTheDocument();
    expect(
      within(screen.getByTestId('model-card-model-a')).getByText('$1'),
    ).toBeInTheDocument();
  });

  it('switches token prices between per 1M and per 1K', async () => {
    serve(healthy);
    renderPage();
    const card = await screen.findByTestId('model-card-model-a');
    expect(within(card).getAllByText(/\/ 1M/).length).toBe(2);
    await userEvent.setup().click(screen.getByTestId('models-unit-K'));
    const after = screen.getByTestId('model-card-model-a');
    expect(within(after).getByText('$0.001')).toBeInTheDocument();
    expect(within(after).getByText('$0.003')).toBeInTheDocument();
    expect(within(after).getAllByText(/\/ 1K/).length).toBe(2);
  });

  it('opens the detail drawer with a quick start per endpoint and language', async () => {
    serve(healthy);
    renderPage();
    const user = userEvent.setup();
    await user.click(await screen.findByTestId('model-card-model-a'));
    await user.click(await screen.findByRole('tab', { name: 'API' }));
    const code = await screen.findByTestId('model-drawer-code');
    expect(code.textContent).toContain('/v1/chat/completions');
    expect(code.textContent).toContain('"model": "model-a"');
    await user.click(screen.getByRole('tab', { name: 'Python' }));
    expect(screen.getByTestId('model-drawer-code').textContent).toContain(
      'requests.post(',
    );
  });

  it('lists the price under every group the caller may use in the drawer', async () => {
    serve(healthy);
    renderPage();
    const user = userEvent.setup();
    await user.click(await screen.findByTestId('model-card-model-a'));
    const groups = await screen.findByTestId('model-drawer-groups');
    // vip x0.5 -> $1 in / $3 out; default x1 -> $2 in / $6 out
    expect(within(groups).getByText('x0.5')).toBeInTheDocument();
    expect(within(groups).getByText('$6')).toBeInTheDocument();
  });

  it('explains an unpriced model in the drawer instead of showing a price', async () => {
    serve(healthy);
    renderPage();
    const user = userEvent.setup();
    await user.click(await screen.findByTestId('model-card-free-z'));
    expect(await screen.findByTestId('model-drawer-unpriced')).toHaveTextContent(
      'not free',
    );
    expect(screen.queryByTestId('model-drawer-groups')).not.toBeInTheDocument();
  });

  it('shows measured success rate and latency, and refuses to guess without samples', async () => {
    serve({
      ...healthy,
      '/models/performance': {
        items: [
          measured,
          {
            model_name: 'free-z',
            p50_latency_ms: 90,
            error_rate: 0.5,
            enough_samples: false,
          },
        ],
      },
    });
    renderPage();
    const perf = await screen.findByTestId('model-perf-model-a');
    expect(perf).toHaveTextContent('98.00%');
    expect(perf).toHaveTextContent('850ms');
    expect(perf).toHaveTextContent('1.50s');
    const thin = screen.getByTestId('model-perf-free-z');
    expect(thin).toHaveTextContent('Too little traffic to measure');
    expect(thin).not.toHaveTextContent('%');
  });

  it('re-reads latency for the chosen window', async () => {
    serve(healthy);
    renderPage();
    await screen.findByTestId('model-card-model-a');
    expect(
      get.mock.calls.some(([p]) => p === '/models/performance?hours=24'),
    ).toBe(true);
    await userEvent.setup().click(screen.getByTestId('models-window-168'));
    await waitFor(() =>
      expect(
        get.mock.calls.some(([p]) => p === '/models/performance?hours=168'),
      ).toBe(true),
    );
    // the page stays up while the next window loads
    expect(screen.getByTestId('model-card-model-a')).toBeInTheDocument();
  });

  it('shows an empty state only when every read succeeded and returned nothing', async () => {
    serve({
      ...healthy,
      '/models/routable': { items: [] },
      '/pricing': { pricing: [], group_ratio: {} },
      '/analytics/rankings': { rows: [] },
    });
    renderPage();
    expect(await screen.findByText('No models yet')).toBeInTheDocument();
    expect(screen.queryByTestId('models-error')).not.toBeInTheDocument();
  });

  it('shows an error, not an empty list, when a required read fails', async () => {
    serve({
      ...healthy,
      '/pricing': new ApiError('pricing backend down', { status: 500 }),
    });
    renderPage();
    const err = await screen.findByTestId('models-error');
    expect(err).toHaveTextContent('Failed to load models');
    expect(err).toHaveTextContent('pricing backend down');
    expect(screen.queryByText('No models yet')).not.toBeInTheDocument();
    expect(screen.queryByTestId('models-rail')).not.toBeInTheDocument();
  });

  it('does not claim 0 tokens when the usage read is refused', async () => {
    serve({
      ...healthy,
      '/analytics/rankings': new ApiError('forbidden', { status: 403 }),
    });
    renderPage();
    const card = await screen.findByTestId('model-card-model-a');
    await waitFor(() => expect(get).toHaveBeenCalled());
    expect(within(card).queryByText(/tokens · 7d/)).not.toBeInTheDocument();
    const sort = screen.getByTestId('models-sort');
    expect(within(sort).queryByText('Most used')).not.toBeInTheDocument();
    expect(sort).toHaveValue('name');
  });

  it('never requests rankings for a non-admin user and hides usage', async () => {
    serve({ ...healthy, '/user/me': { group: 'vip', role: 1 } });
    renderPage();
    const card = await screen.findByTestId('model-card-model-a');
    expect(
      get.mock.calls.some(([p]) => String(p).startsWith('/analytics/rankings')),
    ).toBe(false);
    expect(within(card).queryByText(/tokens · 7d/)).not.toBeInTheDocument();
    expect(screen.getByTestId('models-sort')).toHaveValue('name');
  });

  it('shows a known zero when an admin read succeeds with no rows for a model', async () => {
    serve({ ...healthy, '/analytics/rankings': { rows: [] } });
    renderPage();
    const card = await screen.findByTestId('model-card-model-a');
    expect(within(card).getByText('0 tokens · 7d')).toBeInTheDocument();
    expect(screen.getByTestId('models-sort')).toHaveValue('popular');
  });

  it('shows prices in the display currency from /api/status', async () => {
    apiGet.mockResolvedValue({
      quota_per_unit: 500000,
      quota_display_type: 'CNY',
      usd_exchange_rate: 7,
    });
    serve(healthy);
    renderPage();
    const card = await screen.findByTestId('model-card-model-a');
    expect(within(card).getByText('¥7')).toBeInTheDocument();
  });

  it('shows an error, not prices, when the unit price is unavailable', async () => {
    apiGet.mockResolvedValue({});
    serve(healthy);
    renderPage();
    const err = await screen.findByTestId('models-error');
    expect(err).toHaveTextContent('Price configuration is unavailable');
    expect(screen.queryByTestId('model-card-model-a')).not.toBeInTheDocument();
  });

  it('renders Not configured vs Free for search-unit prices, plus modality', async () => {
    serve({
      ...healthy,
      '/pricing': {
        pricing: [
          {
            model_name: 'rr-unset',
            vendor: 'VendorA',
            modality: 'rerank',
            usage_unit: 'search_unit',
          },
          {
            model_name: 'rr-free',
            vendor: 'VendorA',
            modality: 'rerank',
            usage_unit: 'search_unit',
            search_unit_price: 0,
          },
          {
            model_name: 'rr-paid',
            vendor: 'VendorA',
            modality: 'rerank',
            usage_unit: 'search_unit',
            search_unit_price: 0.002,
          },
        ],
        group_ratio: { default: 1 },
      },
    });
    renderPage();
    await screen.findByTestId('model-card-rr-unset');
    expect(
      within(screen.getByTestId('model-card-rr-unset')).getByText(
        'Not configured',
      ),
    ).toBeInTheDocument();
    expect(
      within(screen.getByTestId('model-card-rr-free')).getByText('Free'),
    ).toBeInTheDocument();
    expect(
      within(screen.getByTestId('model-card-rr-free')).queryByText(
        'Not configured',
      ),
    ).toBeNull();
    expect(
      within(screen.getByTestId('model-card-rr-paid')).getByText('$0.002'),
    ).toBeInTheDocument();

    await userEvent.setup().click(screen.getByRole('button', { name: 'Table view' }));
    expect(screen.getByTestId('model-modality-rr-free')).toHaveTextContent(
      'Rerank',
    );
    expect(screen.getByTestId('model-unit-rr-free')).toHaveTextContent(
      'Per search unit',
    );
  });

  it('pages the card grid', async () => {
    const many = Array.from({ length: 14 }, (_, i) => ({
      model_name: `bulk-${String(i).padStart(2, '0')}`,
      vendor: 'VendorA',
      model_ratio: 1,
    }));
    serve({
      ...healthy,
      '/pricing': { pricing: many, group_ratio: { vip: 1 } },
    });
    renderPage();
    await screen.findByTestId('model-card-bulk-00');
    expect(screen.queryByTestId('model-card-bulk-13')).not.toBeInTheDocument();
    expect(screen.getByTestId('models-page-info')).toHaveTextContent(
      'Page 1 of 2',
    );
    await userEvent.setup().click(screen.getByRole('button', { name: /Next page/ }));
    expect(screen.getByTestId('model-card-bulk-13')).toBeInTheDocument();
  });
});
