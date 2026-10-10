import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { beforeEach, describe, expect, it, vi } from 'vitest'

import { ApiError } from '@/lib/api-error'

import * as api from '../api'
import { LogBodySection } from './log-body-section'

vi.mock('../api', () => ({ fetchLogBody: vi.fn() }))

function renderSection() {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  return render(
    <QueryClientProvider client={client}>
      <LogBodySection requestId='req-1' />
    </QueryClientProvider>
  )
}

const open = () =>
  userEvent.click(screen.getByRole('button', { name: 'Request and reply text' }))

beforeEach(() => {
  vi.clearAllMocks()
})

describe('LogBodySection', () => {
  it('does not read the body until the section is opened', () => {
    renderSection()
    expect(api.fetchLogBody).not.toHaveBeenCalled()
  })

  it('shows request and reply text with scrollable blocks', async () => {
    vi.mocked(api.fetchLogBody).mockResolvedValue({
      requestBody: 'hello there',
      responseText: 'general reply',
      responseCaptured: true,
      truncated: false,
    })
    renderSection()
    await open()
    expect(await screen.findByTestId('log-body-request')).toHaveTextContent(
      'hello there'
    )
    expect(screen.getByTestId('log-body-response')).toHaveTextContent(
      'general reply'
    )
    expect(screen.getByTestId('log-body-request').className).toContain(
      'overflow-auto'
    )
    expect(screen.queryByText('Shortened for storage')).toBeNull()
  })

  it('flags truncation and an unrecorded reply', async () => {
    vi.mocked(api.fetchLogBody).mockResolvedValue({
      requestBody: 'cut',
      responseText: '',
      responseCaptured: false,
      truncated: true,
    })
    renderSection()
    await open()
    expect(await screen.findByText('Shortened for storage')).toBeInTheDocument()
    expect(screen.getByText('Reply text not recorded')).toBeInTheDocument()
    expect(screen.queryByTestId('log-body-response')).toBeNull()
  })

  it('says "not archived" on 404 and surfaces other failures', async () => {
    vi.mocked(api.fetchLogBody).mockRejectedValue(
      new ApiError('gone', { status: 404 })
    )
    renderSection()
    await open()
    expect(await screen.findByTestId('log-body-missing')).toBeInTheDocument()
  })

  it('does not call a server failure "not archived"', async () => {
    vi.mocked(api.fetchLogBody).mockRejectedValue(
      new ApiError('boom', { status: 500 })
    )
    renderSection()
    await open()
    expect(await screen.findByRole('alert')).toHaveTextContent('boom')
    expect(screen.queryByTestId('log-body-missing')).toBeNull()
  })
})
