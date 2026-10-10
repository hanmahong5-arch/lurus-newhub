import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { beforeEach, describe, expect, it, vi } from 'vitest'

import { ApiError } from '@/lib/api'

import { OrgPolicyPage } from './index'

const { get, put, del } = vi.hoisted(() => ({
  get: vi.fn(),
  put: vi.fn(),
  del: vi.fn(),
}))

vi.mock('@/lib/api', async (orig) => {
  const actual = await orig<typeof import('@/lib/api')>()
  return {
    ...actual,
    tenantApi: { get, post: vi.fn(), put, delete: del },
    http: { get: vi.fn() },
  }
})

const wire = (over: Record<string, unknown> = {}) => ({
  public_model: 'model-a',
  enabled: false,
  evaluator_model: 'model-e',
  instructions: '',
  min_confidence: 0.65,
  default_candidate: '',
  candidates: [{ id: 'c1', model: 'model-a', criteria: 'simple' }],
  ...over,
})

function serve(policy: unknown) {
  get.mockImplementation(async (path: string) => {
    if (path === '/models/routable') {
      return { items: [{ id: 'model-a' }, { id: 'model-b' }] }
    }
    if (path === '/routing-policies/model-a') {
      if (policy === null) throw new ApiError('nf', { status: 404 })
      return policy
    }
    return { items: [] }
  })
}

async function openEditor() {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  })
  render(
    <QueryClientProvider client={client}>
      <OrgPolicyPage />
    </QueryClientProvider>
  )
  await userEvent.click(screen.getByRole('tab', { name: 'Decision routing' }))
  await userEvent.click(await screen.findByRole('button', { name: 'model-a' }))
  await screen.findByTestId('routing-editor')
}

beforeEach(() => {
  vi.clearAllMocks()
})

describe('decision routing tab', () => {
  it('shows an empty draft for a model with no policy (404 is not an error)', async () => {
    serve(null)
    await openEditor()
    expect(screen.getByLabelText('Evaluator model')).toHaveValue('')
    expect(screen.queryByRole('button', { name: 'Delete policy' })).toBeNull()
  })

  it('saves a disabled policy straight away with PUT', async () => {
    serve(wire())
    put.mockResolvedValue(wire())
    await openEditor()
    await userEvent.click(screen.getByRole('button', { name: 'Save' }))
    await waitFor(() => expect(put).toHaveBeenCalled())
    expect(put.mock.calls[0][0]).toBe('/routing-policies/model-a')
    expect(put.mock.calls[0][1]).toMatchObject({
      enabled: false,
      evaluator_model: 'model-e',
      min_confidence: 0.65,
    })
  })

  it('enabling needs a second confirmation before PUT', async () => {
    serve(wire())
    put.mockResolvedValue(wire({ enabled: true }))
    await openEditor()
    await userEvent.click(screen.getByRole('switch', { name: 'Enabled' }))
    await userEvent.click(screen.getByRole('button', { name: 'Save' }))
    expect(put).not.toHaveBeenCalled()
    await userEvent.click(await screen.findByRole('button', { name: 'Enable' }))
    await waitFor(() =>
      expect(put.mock.calls[0][1]).toMatchObject({ enabled: true })
    )
  })

  it('blocks an out-of-range threshold on the client', async () => {
    serve(wire())
    await openEditor()
    fireEvent.change(screen.getByLabelText('Confidence threshold'), {
      target: { value: '1.5' },
    })
    await userEvent.click(screen.getByRole('button', { name: 'Save' }))
    expect(
      await screen.findByText(
        'Confidence threshold must be a number from 0 to 1.'
      )
    ).toBeInTheDocument()
    expect(put).not.toHaveBeenCalled()
  })

  it('blocks criteria over 2048 bytes on the client', async () => {
    serve(wire())
    await openEditor()
    fireEvent.change(screen.getByLabelText('Candidate 1 criteria'), {
      target: { value: 'a'.repeat(2049) },
    })
    await userEvent.click(screen.getByRole('button', { name: 'Save' }))
    expect(
      await screen.findByText(
        'Candidate 1: the criteria can be at most 2048 bytes.'
      )
    ).toBeInTheDocument()
    expect(put).not.toHaveBeenCalled()
  })

  it('stops adding candidates at 32', async () => {
    const many = Array.from({ length: 32 }, (_, i) => ({
      id: `c${i}`,
      model: 'model-a',
      criteria: 'x',
    }))
    serve(wire({ candidates: many }))
    await openEditor()
    expect(screen.getByRole('button', { name: 'Add candidate' })).toBeDisabled()
    expect(screen.getByText('Candidates (32/32)')).toBeInTheDocument()
  })

  it('deleting asks first, then DELETEs', async () => {
    serve(wire())
    del.mockResolvedValue({})
    await openEditor()
    await userEvent.click(screen.getByRole('button', { name: 'Delete policy' }))
    expect(del).not.toHaveBeenCalled()
    const buttons = await screen.findAllByRole('button', { name: 'Delete' })
    const last = buttons.at(-1)
    if (!last) throw new Error('no confirm button')
    await userEvent.click(last)
    await waitFor(() =>
      expect(del).toHaveBeenCalledWith('/routing-policies/model-a')
    )
  })
})
