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
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { beforeEach, describe, expect, it, vi } from 'vitest'

import { api } from '@/lib/api'
import { ApiError } from '@/lib/api-error'

import { TasksPage } from './index'

vi.mock('@/lib/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/api')>()
  return { ...actual, api: { ...actual.api, get: vi.fn() } }
})
vi.mock('@/lib/status', () => ({
  useMoneyConfig: () => ({
    quotaPerUnit: 500000,
    displayType: 'USD',
    symbol: '$',
    rate: 1,
  }),
}))

const task = (over: Record<string, unknown> = {}) => ({
  id: 1,
  task_id: 'task-abc',
  platform: 'suno',
  action: 'MUSIC',
  status: 'SUCCESS',
  quota: 250000,
  progress: '100%',
  submit_time: 1_800_000_000,
  finish_time: 1_800_000_030,
  fail_reason: 'https://files.example.com/out.mp4',
  properties: { input: 'a calm tune', origin_model_name: 'model-a' },
  data: null,
  ...over,
})

function pageOf(items: unknown[] | null, total = items?.length ?? 0) {
  return { items, total, page: 1, page_size: 20 }
}

function renderPage() {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  return render(
    <QueryClientProvider client={client}>
      <TasksPage />
    </QueryClientProvider>
  )
}

const get = vi.mocked(api.get)

beforeEach(() => {
  get.mockReset()
})

describe('TasksPage', () => {
  it('lists the caller tasks with converted cost and a safe result link', async () => {
    get.mockResolvedValue(pageOf([task()]))
    renderPage()
    expect(await screen.findByText('task-abc')).toBeInTheDocument()
    expect(get).toHaveBeenCalledWith(
      '/api/task/self/',
      expect.objectContaining({
        params: expect.objectContaining({ p: 1, page_size: 20 }),
      })
    )
    // 250000 quota at 500000/unit is $0.5.
    expect(screen.getByText('$0.500000')).toBeInTheDocument()
    const link = screen.getByRole('link', { name: /Open result/ })
    expect(link).toHaveAttribute('href', 'https://files.example.com/out.mp4')
    expect(link).toHaveAttribute('rel', 'noopener noreferrer')
  })

  it('does not treat a failure text as a link', async () => {
    get.mockResolvedValue(
      pageOf([
        task({ status: 'FAILURE', fail_reason: 'https://x.example.com/boom' }),
      ])
    )
    renderPage()
    await screen.findByText('task-abc')
    expect(screen.queryByRole('link', { name: /Open result/ })).toBeNull()
    expect(screen.getByText('https://x.example.com/boom')).toBeInTheDocument()
  })

  it('shows an error, not an empty list, when the read fails', async () => {
    get.mockRejectedValue(new ApiError('tasks unavailable', { status: 500 }))
    renderPage()
    expect(await screen.findByText('tasks unavailable')).toBeInTheDocument()
    expect(screen.queryByText('No tasks yet')).toBeNull()
  })

  it('shows the empty state for a successful empty page', async () => {
    get.mockResolvedValue(pageOf(null))
    renderPage()
    expect(await screen.findByText('No tasks yet')).toBeInTheDocument()
  })

  it('sends the task id filter in seconds-based params after Search', async () => {
    get.mockResolvedValue(pageOf([task()]))
    renderPage()
    await screen.findByText('task-abc')
    const user = userEvent.setup()
    await user.type(screen.getByLabelText('Task ID'), 'task-abc')
    await user.click(screen.getByRole('button', { name: 'Search' }))
    await waitFor(() =>
      expect(get).toHaveBeenLastCalledWith(
        '/api/task/self/',
        expect.objectContaining({
          params: expect.objectContaining({ task_id: 'task-abc' }),
        })
      )
    )
  })

  it('opens the details dialog with the input and the failure reason', async () => {
    get.mockResolvedValue(
      pageOf([task({ status: 'FAILURE', fail_reason: 'quota exceeded' })])
    )
    renderPage()
    const user = userEvent.setup()
    await user.click(await screen.findByTestId('task-row'))
    expect(await screen.findByTestId('task-input')).toHaveTextContent(
      'a calm tune'
    )
    expect(screen.getByTestId('task-fail-reason')).toHaveTextContent(
      'quota exceeded'
    )
  })

  it('the drawing tab reads /api/mj/self/ with millisecond bounds', async () => {
    get.mockImplementation((url: string) =>
      Promise.resolve(
        url.startsWith('/api/mj')
          ? pageOf([
              {
                id: 5,
                action: 'IMAGINE',
                mj_id: 'mj-77',
                status: 'SUCCESS',
                submit_time: 1_800_000_000_000,
                finish_time: 1_800_000_012_000,
                image_url: 'javascript:alert(1)',
                prompt: 'a lighthouse',
              },
            ])
          : pageOf([])
      )
    )
    renderPage()
    const user = userEvent.setup()
    await user.click(await screen.findByRole('tab', { name: 'Drawing logs' }))
    expect(await screen.findByText('mj-77')).toBeInTheDocument()
    expect(get).toHaveBeenCalledWith('/api/mj/self/', expect.anything())
    // A non-http image URL is never rendered as a link.
    expect(screen.queryByRole('link', { name: /Open image/ })).toBeNull()
  })
})
