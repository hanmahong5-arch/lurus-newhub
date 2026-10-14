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
import { render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { beforeAll, beforeEach, describe, expect, it, vi } from 'vitest'

import { ApiError } from '@/lib/api'

import { AnnouncementsPanel, FaqPanel, NoticePanel } from './content-panels'

const { apiGet } = vi.hoisted(() => ({ apiGet: vi.fn() }))

vi.mock('@/lib/api', async (orig) => {
  const actual = await orig<typeof import('@/lib/api')>()
  return { ...actual, api: { get: apiGet } }
})

function renderWith(ui: React.ReactNode) {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  return render(<QueryClientProvider client={client}>{ui}</QueryClientProvider>)
}

function route(over: { status?: Record<string, unknown>; notice?: unknown }) {
  apiGet.mockImplementation((url: string) => {
    if (url === '/api/status') return Promise.resolve(over.status ?? {})
    if (url === '/api/notice') {
      return over.notice instanceof Error
        ? Promise.reject(over.notice)
        : Promise.resolve(over.notice ?? '')
    }
    return Promise.resolve(null)
  })
}

// jsdom has no Web Animations API; the scroll-area viewport asks for it.
beforeAll(() => {
  if (!('getAnimations' in Element.prototype)) {
    Object.defineProperty(Element.prototype, 'getAnimations', {
      configurable: true,
      value: () => [],
    })
  }
})

beforeEach(() => {
  vi.clearAllMocks()
})

describe('AnnouncementsPanel', () => {
  const status = {
    announcements_enabled: true,
    announcements: [
      {
        id: 1,
        content: '**Maintenance** on Friday',
        publishDate: '2024-03-01T10:00:00Z',
        type: 'warning',
        extra: 'Expect a short pause.',
      },
      { id: 2, content: 'New models added', type: 'success' },
    ],
  }

  it('lists the announcements as plain-text previews', async () => {
    route({ status })
    renderWith(<AnnouncementsPanel />)
    expect(await screen.findByText('Maintenance on Friday')).toBeInTheDocument()
    expect(screen.getByText('New models added')).toBeInTheDocument()
  })

  it('opens the full text in a dialog', async () => {
    route({ status })
    const user = userEvent.setup()
    renderWith(<AnnouncementsPanel />)
    await user.click(await screen.findByText('Maintenance on Friday'))
    const dialog = await screen.findByRole('dialog')
    expect(within(dialog).getByText('Announcement Details')).toBeInTheDocument()
    expect(within(dialog).getByText('Expect a short pause.')).toBeInTheDocument()
  })

  it('renders nothing when the operator turned the panel off', async () => {
    route({ status: { announcements_enabled: false } })
    const { container } = renderWith(<AnnouncementsPanel />)
    await new Promise((r) => setTimeout(r, 20))
    expect(container).toBeEmptyDOMElement()
  })

  it('enabled with no entries says there are none', async () => {
    route({ status: { announcements_enabled: true, announcements: [] } })
    renderWith(<AnnouncementsPanel />)
    expect(await screen.findByText('No announcements at this time')).toBeInTheDocument()
  })
})

describe('FaqPanel', () => {
  it('shows the questions when enabled', async () => {
    route({
      status: {
        faq_enabled: true,
        faq: [{ id: 1, question: 'How do I top up?', answer: 'Use the wallet.' }],
      },
    })
    renderWith(<FaqPanel />)
    expect(await screen.findByText('How do I top up?')).toBeInTheDocument()
  })

  it('hidden when disabled', async () => {
    route({ status: { faq_enabled: false, faq: [{ question: 'q', answer: 'a' }] } })
    const { container } = renderWith(<FaqPanel />)
    await new Promise((r) => setTimeout(r, 20))
    expect(container).toBeEmptyDOMElement()
  })
})

describe('NoticePanel', () => {
  it('shows the operator notice', async () => {
    route({ notice: 'Scheduled **downtime** tonight' })
    renderWith(<NoticePanel />)
    expect(await screen.findByTestId('dashboard-notice')).toHaveTextContent(
      'Scheduled downtime tonight'
    )
    expect(apiGet).toHaveBeenCalledWith('/api/notice', { skipAuthRedirect: true })
  })

  it('no notice set means no panel', async () => {
    route({ notice: '' })
    const { container } = renderWith(<NoticePanel />)
    await new Promise((r) => setTimeout(r, 20))
    expect(container).toBeEmptyDOMElement()
  })

  it('a failed read says so instead of hiding silently', async () => {
    route({ notice: new ApiError('nope', { status: 500 }) })
    renderWith(<NoticePanel />)
    expect(await screen.findByTestId('dashboard-load-failed')).toBeInTheDocument()
  })
})
