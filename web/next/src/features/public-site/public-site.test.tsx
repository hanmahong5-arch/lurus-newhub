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
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { useState, type ReactNode } from 'react'
import { beforeEach, describe, expect, it, vi } from 'vitest'

import { ApiError } from '@/lib/api-error'

import { AboutPage } from './about'
import { HomePage } from './home'
import { LegalPage } from './legal'
import { PublicLayout } from './layout'
import { PublicModelDetailPage } from './pricing/detail'
import { PublicPricingPage } from './pricing'
import type { PricingSearch } from './pricing/constants'

const { httpGet, apiGet, tenantGet } = vi.hoisted(() => ({
  httpGet: vi.fn(),
  apiGet: vi.fn(),
  tenantGet: vi.fn(),
}))

vi.mock('@/lib/api', async (orig) => {
  const mod = await orig<typeof import('@/lib/api')>()
  return {
    ...mod,
    http: { ...mod.http, get: httpGet },
    api: { ...mod.api, get: apiGet },
    tenantApi: { ...mod.tenantApi, get: tenantGet },
  }
})

vi.mock('@tanstack/react-router', () => ({
  Outlet: () => <div>routed page</div>,
  useRouterState: ({ select }: { select: (s: unknown) => unknown }) =>
    select({ location: { pathname: '/' } }),
  Link: ({
    to,
    params,
    search: _search,
    children,
    ...rest
  }: {
    to: string
    params?: Record<string, string>
    search?: unknown
    children?: ReactNode
  }) => {
    const href = Object.entries(params ?? {}).reduce(
      (acc, [key, value]) => acc.replace(`$${key}`, String(value)),
      to
    )
    return (
      <a href={href} {...rest}>
        {children}
      </a>
    )
  },
}))

const STATUS = {
  system_name: 'Acme Gateway',
  quota_per_unit: 500000,
  quota_display_type: 'USD',
  user_agreement_enabled: true,
  privacy_policy_enabled: false,
  docs_link: 'https://docs.example/start',
  server_address: 'https://gw.example',
}

const PRICING_BODY = {
  success: true,
  data: [
    {
      model_name: 'model-a',
      vendor_id: 1,
      description: 'General purpose model',
      tags: 'fast',
      quota_type: 0,
      model_ratio: 1,
      completion_ratio: 3,
      enable_groups: ['default', 'vip'],
      supported_endpoint_types: ['openai'],
      price_configured: true,
    },
    {
      model_name: 'embed-b',
      vendor_id: 2,
      quota_type: 0,
      model_ratio: 0.25,
      completion_ratio: 0,
      enable_groups: ['default'],
      supported_endpoint_types: ['embeddings'],
      modality: 'embedding',
      price_configured: true,
    },
    {
      model_name: 'model-unpriced',
      vendor_id: 1,
      quota_type: 0,
      model_ratio: 0,
      enable_groups: ['default'],
      supported_endpoint_types: ['openai'],
      price_configured: false,
    },
  ],
  vendors: [
    { id: 1, name: 'Vendor A' },
    { id: 2, name: 'Vendor B' },
  ],
  group_ratio: { default: 1, vip: 0.5 },
  usable_group: { default: 'Default', vip: 'VIP' },
  supported_endpoint: {},
}

interface World {
  status: Record<string, unknown>
  home: string
  about: string
  agreement: string
  privacy: string
  signedIn: boolean
  pricing: () => unknown
}

let world: World

function resetWorld() {
  world = {
    status: { ...STATUS },
    home: '',
    about: '',
    agreement: '',
    privacy: '',
    signedIn: false,
    pricing: () => ({ data: PRICING_BODY, status: 200 }),
  }
  httpGet.mockReset()
  apiGet.mockReset()
  tenantGet.mockReset()
  httpGet.mockImplementation(async (url: string) => {
    if (url === '/api/pricing') {
      const out = world.pricing()
      if (out instanceof Error) throw out
      return out
    }
    throw new Error(`unexpected http.get ${url}`)
  })
  apiGet.mockImplementation(async (url: string) => {
    switch (url) {
      case '/api/status':
        return world.status
      case '/api/home_page_content':
        return world.home
      case '/api/about':
        return world.about
      case '/api/user-agreement':
        return world.agreement
      case '/api/privacy-policy':
        return world.privacy
      case '/api/v2/public/model-status':
        return {
          models: [{ model: 'model-a', status: 'operational' }],
          updated_at: 1_700_000_000,
        }
      default:
        throw new Error(`unexpected api.get ${url}`)
    }
  })
  tenantGet.mockImplementation(async () => {
    if (!world.signedIn) throw new ApiError('unauthorized', { status: 401 })
    return { id: 1, username: 'person', role: 1 }
  })
}

function renderWith(node: ReactNode) {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false, gcTime: 0 } },
  })
  return render(<QueryClientProvider client={client}>{node}</QueryClientProvider>)
}

beforeEach(() => {
  resetWorld()
})

describe('PublicLayout', () => {
  it('sends a visitor without a session to sign-in and never redirects on 401', async () => {
    renderWith(<PublicLayout />)
    expect(await screen.findByText('routed page')).toBeInTheDocument()

    const consoleLink = await screen.findByRole('link', { name: 'Console' })
    expect(consoleLink).toHaveAttribute(
      'href',
      '/login?redirect=%2Fdashboard'
    )
    await waitFor(() => expect(tenantGet).toHaveBeenCalled())
    // The probe opts out of the redirect in lib/api.ts.
    expect(tenantGet).toHaveBeenCalledWith('/user/me', {
      skipAuthRedirect: true,
    })
  })

  it('links a signed-in visitor straight to the console', async () => {
    world.signedIn = true
    renderWith(<PublicLayout />)
    await waitFor(() =>
      expect(screen.getByRole('link', { name: 'Console' })).toHaveAttribute(
        'href',
        '/dashboard'
      )
    )
  })

  it('shows legal links only for documents the operator enabled, plus the attribution', async () => {
    renderWith(<PublicLayout />)
    expect(
      await screen.findByRole('link', { name: 'User Agreement' })
    ).toHaveAttribute('href', '/user-agreement')
    expect(
      screen.queryByRole('link', { name: 'Privacy Policy' })
    ).not.toBeInTheDocument()
    const attribution = screen.getByRole('link', {
      name: 'Based on New API (AGPL-3.0)',
    })
    expect(attribution).toHaveAttribute(
      'href',
      'https://github.com/QuantumNous/new-api'
    )
    expect(screen.getAllByText(/Acme Gateway/).length).toBeGreaterThan(0)
  })

  it('hides the price link when the operator turned the module off', async () => {
    world.status.HeaderNavModules = JSON.stringify({
      pricing: { enabled: false, requireAuth: false },
    })
    renderWith(<PublicLayout />)
    await screen.findByText('routed page')
    await waitFor(() =>
      expect(
        screen.queryByRole('link', { name: 'Pricing' })
      ).not.toBeInTheDocument()
    )
    expect(screen.getAllByRole('link', { name: 'About' }).length).toBeGreaterThan(0)
  })
})

describe('HomePage', () => {
  it('shows the built-in landing, with live counts from the price list', async () => {
    renderWith(<HomePage />)
    expect(
      await screen.findByRole('heading', {
        level: 1,
        name: /One OpenAI-compatible endpoint/,
      })
    ).toBeInTheDocument()
    // Two priced models from two vendors, read from the catalogue.
    const strip = await screen.findByLabelText('Live catalogue')
    expect(within(strip).getByText('priced models')).toBeInTheDocument()
    expect(within(strip).getByText('vendors').nextSibling).toHaveTextContent('2')
  })

  it('keeps the landing for signed-in visitors and says "Go to console"', async () => {
    world.signedIn = true
    renderWith(<HomePage />)
    await waitFor(() => {
      const links = screen.getAllByRole('link', { name: /Go to console/ })
      expect(links.length).toBeGreaterThan(0)
      for (const link of links) expect(link).toHaveAttribute('href', '/dashboard')
    })
  })

  it('renders operator markdown instead of the landing', async () => {
    world.home = '# Welcome to our gateway\n\nA custom intro.'
    renderWith(<HomePage />)
    expect(
      await screen.findByRole('heading', { name: 'Welcome to our gateway' })
    ).toBeInTheDocument()
    expect(
      screen.queryByRole('heading', { name: /One OpenAI-compatible endpoint/ })
    ).not.toBeInTheDocument()
  })

  it('embeds an operator URL in a sandboxed frame', async () => {
    world.home = 'https://home.example/landing'
    renderWith(<HomePage />)
    const frame = await screen.findByTitle('Custom Home Page')
    expect(frame).toHaveAttribute('src', 'https://home.example/landing')
    expect(frame.getAttribute('sandbox')).not.toContain('allow-same-origin')
  })
})

describe('AboutPage and LegalPage', () => {
  it('falls back to the built-in text with the New API attribution', async () => {
    renderWith(<AboutPage />)
    expect(
      await screen.findByRole('heading', { name: 'About Acme Gateway' })
    ).toBeInTheDocument()
    expect(screen.getByRole('link', { name: 'New API' })).toHaveAttribute(
      'href',
      'https://github.com/QuantumNous/new-api'
    )
    expect(screen.getByText('GNU AGPL v3.0')).toBeInTheDocument()
  })

  it('renders the operator about page when one is set', async () => {
    world.about = '## Our story\n\nFounded last year.'
    renderWith(<AboutPage />)
    expect(
      await screen.findByRole('heading', { name: 'Our story' })
    ).toBeInTheDocument()
  })

  it('reports a failed about request with a retry', async () => {
    apiGet.mockImplementation(async (url: string) => {
      if (url === '/api/about') throw new ApiError('boom', { status: 500 })
      return world.status
    })
    renderWith(<AboutPage />)
    expect(await screen.findByText('Failed to load this page')).toBeVisible()
    expect(screen.getByText('boom')).toBeVisible()
    expect(screen.getByRole('button', { name: 'Retry' })).toBeVisible()
  })

  it('shows a legal document with its title', async () => {
    world.agreement = 'Be excellent to each other.'
    renderWith(<LegalPage kind='user-agreement' />)
    expect(
      await screen.findByRole('heading', { name: 'User Agreement' })
    ).toBeInTheDocument()
    expect(screen.getByText('Be excellent to each other.')).toBeInTheDocument()
  })

  it('says so when a legal document was never written', async () => {
    renderWith(<LegalPage kind='privacy-policy' />)
    expect(
      await screen.findByText(
        'The administrator has not configured a privacy policy yet.'
      )
    ).toBeInTheDocument()
  })
})

function PricingHarness(props: { initial?: PricingSearch }) {
  const [search, setSearch] = useState<PricingSearch>(props.initial ?? {})
  return (
    <PublicPricingPage
      search={search}
      onSearchChange={setSearch}
      onOpenModel={vi.fn()}
    />
  )
}

describe('PublicPricingPage', () => {
  it('lists priced models with prices from the shared money helpers', async () => {
    renderWith(<PricingHarness />)
    const card = await screen.findByTestId('model-card-model-a')
    // ratio 1 x $2 per ratio unit x best group ratio 0.5 = $1 in, $3 out.
    expect(within(card).getByText('$1')).toBeInTheDocument()
    expect(within(card).getByText('$3')).toBeInTheDocument()
    expect(within(card).getByText('Vendor A')).toBeInTheDocument()
    expect(within(card).getByRole('link', { name: /Details of model-a/ })).toHaveAttribute(
      'href',
      '/pricing/model-a'
    )
    // Operational dot comes from the public status endpoint.
    await waitFor(() =>
      expect(within(card).getByTestId('model-health')).toHaveTextContent(
        'Operational'
      )
    )
    expect(screen.getByText('2 models have a published price')).toBeVisible()
  })

  it('never shows a model nobody priced as free', async () => {
    renderWith(<PricingHarness />)
    const card = await screen.findByTestId('model-card-model-unpriced')
    expect(within(card).getByText('Unpriced')).toBeInTheDocument()
    expect(within(card).queryByText(/Free|\$0/)).not.toBeInTheDocument()
  })

  it('filters by search text', async () => {
    const user = userEvent.setup()
    renderWith(<PricingHarness />)
    await screen.findByTestId('model-card-model-a')
    await user.type(screen.getByRole('searchbox', { name: 'Search models' }), 'embed')
    await waitFor(() => {
      expect(screen.queryByTestId('model-card-model-a')).not.toBeInTheDocument()
    })
    expect(screen.getByTestId('model-card-embed-b')).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: 'Clear search' }))
    expect(await screen.findByTestId('model-card-model-a')).toBeInTheDocument()
  })

  it('filters by a sidebar chip and resets', async () => {
    const user = userEvent.setup()
    renderWith(<PricingHarness />)
    await screen.findByTestId('model-card-model-a')
    const sidebar = screen.getAllByRole('complementary', { name: 'Filter' })[0]
    await user.click(within(sidebar).getByRole('button', { name: /Vendor B/ }))
    await waitFor(() =>
      expect(screen.queryByTestId('model-card-model-a')).not.toBeInTheDocument()
    )
    expect(screen.getByTestId('model-card-embed-b')).toBeInTheDocument()
    await user.click(within(sidebar).getByRole('button', { name: /Reset/ }))
    expect(await screen.findByTestId('model-card-model-a')).toBeInTheDocument()
  })

  it('prices a chosen group at that group ratio', async () => {
    renderWith(<PricingHarness initial={{ group: 'default' }} />)
    const card = await screen.findByTestId('model-card-model-a')
    // default group is x1: $2 in, $6 out.
    expect(within(card).getByText('$2')).toBeInTheDocument()
    expect(within(card).getByText('$6')).toBeInTheDocument()
  })

  it('switches to the table view with per-model rows', async () => {
    renderWith(<PricingHarness initial={{ view: 'table' }} />)
    const link = await screen.findByRole('link', { name: 'model-a' })
    const row = link.closest('tr')
    expect(row).not.toBeNull()
    expect(within(row as HTMLElement).getByText('$1')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'View' })).toBeInTheDocument()
  })

  it('explains an empty result and clears the filters', async () => {
    const user = userEvent.setup()
    renderWith(<PricingHarness initial={{ search: 'zzz-nothing' }} />)
    expect(await screen.findByText('No models found')).toBeVisible()
    await user.click(screen.getByRole('button', { name: 'Clear all filters' }))
    expect(await screen.findByTestId('model-card-model-a')).toBeInTheDocument()
  })

  it('reports a failed load and recovers on retry', async () => {
    const user = userEvent.setup()
    world.pricing = () => new ApiError('pricing is down', { status: 500 })
    renderWith(<PricingHarness />)
    expect(await screen.findByText('Failed to load the price list')).toBeVisible()
    expect(screen.getByText('pricing is down')).toBeVisible()

    world.pricing = () => ({ data: PRICING_BODY, status: 200 })
    await user.click(screen.getByRole('button', { name: 'Retry' }))
    expect(await screen.findByTestId('model-card-model-a')).toBeInTheDocument()
  })

  it('surfaces an unsuccessful envelope as an error, not an empty list', async () => {
    world.pricing = () => ({
      data: { success: false, message: 'catalogue unavailable' },
      status: 200,
    })
    renderWith(<PricingHarness />)
    expect(await screen.findByText('catalogue unavailable')).toBeVisible()
  })

  it('marks token prices unavailable when the site settings failed to load', async () => {
    world.status = { system_name: 'Acme Gateway' }
    renderWith(<PricingHarness />)
    expect(await screen.findByText('Token prices are unavailable')).toBeVisible()
    const card = await screen.findByTestId('model-card-model-a')
    expect(within(card).getByText('Price unavailable')).toBeInTheDocument()
  })

  it('asks for sign-in when the operator requires it', async () => {
    world.status.HeaderNavModules = JSON.stringify({
      pricing: { enabled: true, requireAuth: true },
    })
    renderWith(<PricingHarness />)
    const link = await screen.findByRole('link', { name: 'Sign in' })
    expect(link).toHaveAttribute('href', '/login?redirect=%2Fpricing')
    expect(screen.queryByTestId('model-card-model-a')).not.toBeInTheDocument()
  })

  it('says the page is off when the operator disabled the module', async () => {
    world.status.HeaderNavModules = JSON.stringify({ pricing: false })
    renderWith(<PricingHarness />)
    expect(await screen.findByText('The price list is not available')).toBeVisible()
  })
})

describe('PublicModelDetailPage', () => {
  it('shows the price for every group', async () => {
    renderWith(<PublicModelDetailPage modelId='model-a' search={{}} />)
    expect(
      await screen.findByRole('heading', { level: 1, name: 'model-a' })
    ).toBeInTheDocument()
    const table = await screen.findByRole('table')
    const defaultRow = within(table).getByText('default').closest('tr') as HTMLElement
    const vipRow = within(table).getByText('vip').closest('tr') as HTMLElement
    expect(within(defaultRow).getByText('$2')).toBeInTheDocument()
    expect(within(defaultRow).getByText('$6')).toBeInTheDocument()
    expect(within(vipRow).getByText('$1')).toBeInTheDocument()
    expect(within(vipRow).getByText('$3')).toBeInTheDocument()
    // No cache price from this server: no cache column.
    expect(within(table).queryByText(/Cached/)).not.toBeInTheDocument()
    expect(screen.getByRole('link', { name: /Back to Models/ })).toHaveAttribute(
      'href',
      '/pricing'
    )
  })

  it('gives a ready-to-copy request for the model', async () => {
    const user = userEvent.setup()
    renderWith(<PublicModelDetailPage modelId='model-a' search={{}} />)
    await user.click(await screen.findByRole('tab', { name: 'API' }))
    const sample = await screen.findByLabelText('Example request', {
      selector: 'pre',
    })
    expect(sample).toHaveTextContent('https://gw.example/v1/chat/completions')
    expect(sample).toHaveTextContent('"model":"model-a"')
    expect(sample).toHaveTextContent('$API_KEY')
  })

  it('shows the health state without inventing metrics', async () => {
    const user = userEvent.setup()
    renderWith(<PublicModelDetailPage modelId='model-a' search={{}} />)
    await user.click(await screen.findByRole('tab', { name: 'Status' }))
    expect(await screen.findByText('Operational')).toBeVisible()
    expect(screen.queryByText(/latency|uptime|tps/i)).not.toBeInTheDocument()
  })

  it('says so when the model is not on the public list', async () => {
    renderWith(<PublicModelDetailPage modelId='no-such-model' search={{}} />)
    expect(await screen.findByText('Model not found')).toBeVisible()
  })
})
