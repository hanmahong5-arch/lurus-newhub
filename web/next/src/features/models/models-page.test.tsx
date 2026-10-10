import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { beforeEach, describe, expect, it, vi } from 'vitest';

import { ApiError } from '@/lib/api-error';

const { get, apiGet } = vi.hoisted(() => ({ get: vi.fn(), apiGet: vi.fn() }));
vi.mock('@/lib/api', async (orig) => {
  const mod = await orig<typeof import('@/lib/api')>();
  return {
    ...mod,
    api: { ...mod.api, get: apiGet },
    tenantApi: { ...mod.tenantApi, get },
  };
});

import { ModelsPage } from './index';
import { WIRE_CHAT } from './lib/wire';

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
      },
      { model_name: 'free-z', vendor: 'VendorB', model_ratio: 0 },
    ],
    group_ratio: { vip: 0.5, default: 1 },
  },
  '/models?': { items: [], total: 0 },
  '/analytics/rankings': {
    rows: [{ name: 'model-a', total_tokens: 2500, requests: 4 }],
  },
  '/models/performance': { items: [] },
};

describe('ModelsPage', () => {
  beforeEach(() => {
    get.mockReset();
    apiGet.mockReset();
    apiGet.mockResolvedValue({ quota_per_unit: 500000 });
  });

  it('shows prices after the group multiplier and Unpriced instead of $0', async () => {
    serve(healthy);
    renderPage();
    const card = await screen.findByTestId('model-card-model-a');
    // ratio 1 -> $2/M, group vip 0.5 -> $1/M in, completion 3 -> $3/M out
    expect(within(card).getByText(/\$1 \/M input/)).toBeInTheDocument();
    expect(within(card).getByText(/\$3 \/M output/)).toBeInTheDocument();
    expect(within(card).getByText('2.5K tokens · 7d')).toBeInTheDocument();

    const free = screen.getByTestId('model-card-free-z');
    expect(within(free).getByText('Unpriced')).toBeInTheDocument();
    expect(free.textContent).not.toContain('$0');
  });

  it('filters by vendor and switches to the table view', async () => {
    serve(healthy);
    renderPage();
    await screen.findByTestId('model-card-model-a');
    const user = userEvent.setup();
    await user.click(
      within(screen.getByTestId('vendor-facet-VendorB')).getByRole('checkbox'),
    );
    expect(screen.queryByTestId('model-card-model-a')).not.toBeInTheDocument();
    expect(screen.getByTestId('model-card-free-z')).toBeInTheDocument();
    await user.click(screen.getByTestId('models-view-table'));
    expect(screen.getByTestId('model-row-free-z')).toBeInTheDocument();
  });

  it('opens the detail drawer with a quick start', async () => {
    serve(healthy);
    renderPage();
    const user = userEvent.setup();
    await user.click(await screen.findByTestId('model-card-model-a'));
    const code = await screen.findByTestId('model-drawer-code');
    expect(code.textContent).toContain('/v1/chat/completions');
    expect(code.textContent).toContain('"model": "model-a"');
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
    expect(within(card).getByText(/¥7 \/M input/)).toBeInTheDocument();
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

    await userEvent.setup().click(screen.getByTestId('models-view-table'));
    expect(screen.getByTestId('model-modality-rr-free')).toHaveTextContent(
      'Rerank',
    );
    expect(screen.getByTestId('model-unit-rr-free')).toHaveTextContent(
      'Per search unit',
    );
  });
});
