import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { beforeEach, describe, expect, it, vi } from 'vitest'

import { ApiError } from '@/lib/api'

import { OrgPolicyPage } from './index'

const { get, put } = vi.hoisted(() => ({
  get: vi.fn(),
  put: vi.fn(),
}))

vi.mock('@/lib/api', async (orig) => {
  const actual = await orig<typeof import('@/lib/api')>()
  return {
    ...actual,
    tenantApi: { get, post: vi.fn(), put, delete: vi.fn() },
    http: { get: vi.fn() },
  }
})

const decisionWire = (over: Record<string, unknown> = {}) => ({
  public_model: 'model-a',
  strategy: 'decision',
  enabled: false,
  evaluator_model: 'model-e',
  instructions: '',
  min_confidence: 0.65,
  default_candidate: '',
  candidates: [{ id: 'c1', model: 'model-a', criteria: 'simple' }],
  ...over,
})

const fallbackWire = (over: Record<string, unknown> = {}) => ({
  public_model: 'model-a',
  strategy: 'fallback',
  enabled: false,
  candidates: [
    { id: 'backup-1', model: 'model-b', criteria: '' },
    { id: 'backup-2', model: 'model-c', criteria: '' },
  ],
  ...over,
})

function serve(policy: unknown) {
  get.mockImplementation(async (path: string) => {
    if (path === '/models/routable') {
      return {
        items: [{ id: 'model-a' }, { id: 'model-b' }, { id: 'model-c' }],
      }
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
  await userEvent.click(screen.getByRole('tab', { name: 'Model routing' }))
  await userEvent.click(await screen.findByRole('button', { name: 'model-a' }))
  await screen.findByTestId('routing-editor')
}

const click = (name: string, role: 'button' | 'radio' | 'switch' = 'button') =>
  userEvent.click(screen.getByRole(role, { name }))

function backupModels(): string[] {
  const out: string[] = []
  for (let n = 1; n <= 8; n++) {
    const el = screen.queryByLabelText(`Backup ${n} model`)
    if (el) out.push((el as HTMLSelectElement).value)
  }
  return out
}

beforeEach(() => {
  vi.clearAllMocks()
})

describe('strategy selector', () => {
  it('defaults a new policy to decision and shows its fields', async () => {
    serve(null)
    await openEditor()
    expect(
      screen.getByRole('radio', { name: 'Decision routing' })
    ).toBeChecked()
    expect(screen.getByLabelText('Evaluator model')).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'Add backup model' })).toBeNull()
  })

  it('explains the fallback strategy', async () => {
    serve(null)
    await openEditor()
    expect(
      screen.getByText(
        /first available backup model, billed at the backup model's price/
      )
    ).toBeInTheDocument()
  })

  it('switching to fallback hides every decision-only field', async () => {
    serve(decisionWire())
    await openEditor()
    await click('Fallback', 'radio')
    expect(screen.getByRole('radio', { name: 'Fallback' })).toBeChecked()
    expect(screen.queryByLabelText('Evaluator model')).toBeNull()
    expect(screen.queryByLabelText('Confidence threshold')).toBeNull()
    expect(screen.queryByLabelText('Instructions')).toBeNull()
    expect(screen.queryByLabelText('Default candidate')).toBeNull()
    expect(screen.queryByLabelText('Candidate 1 criteria')).toBeNull()
    expect(
      screen.getByRole('button', { name: 'Add backup model' })
    ).toBeInTheDocument()
  })

  it('switching back to decision keeps what was typed', async () => {
    serve(decisionWire())
    await openEditor()
    fireEvent.change(screen.getByLabelText('Evaluator model'), {
      target: { value: 'model-z' },
    })
    await click('Fallback', 'radio')
    await click('Decision routing', 'radio')
    expect(screen.getByLabelText('Evaluator model')).toHaveValue('model-z')
  })
})

describe('fallback editor', () => {
  it('loads a stored fallback policy in order', async () => {
    serve(fallbackWire())
    await openEditor()
    expect(screen.getByRole('radio', { name: 'Fallback' })).toBeChecked()
    expect(backupModels()).toEqual(['model-b', 'model-c'])
  })

  it('the picker offers routable models except the protected one', async () => {
    serve(fallbackWire())
    await openEditor()
    const select = screen.getByLabelText('Backup 1 model') as HTMLSelectElement
    const opts = [...select.options].map((o) => o.value)
    expect(opts).toContain('model-b')
    expect(opts).toContain('model-c')
    expect(opts).not.toContain('model-a')
  })

  it('adds, reorders and removes backups', async () => {
    serve(fallbackWire())
    await openEditor()
    await click('Move backup 2 up')
    expect(backupModels()).toEqual(['model-c', 'model-b'])
    await click('Move backup 1 down')
    expect(backupModels()).toEqual(['model-b', 'model-c'])
    await click('Add backup model')
    expect(backupModels()).toEqual(['model-b', 'model-c', ''])
    await click('Remove backup 1')
    expect(backupModels()).toEqual(['model-c', ''])
  })

  it('disables the first up and the last down buttons', async () => {
    serve(fallbackWire())
    await openEditor()
    expect(screen.getByRole('button', { name: 'Move backup 1 up' })).toBeDisabled()
    expect(
      screen.getByRole('button', { name: 'Move backup 2 down' })
    ).toBeDisabled()
  })

  it('stops adding backups at 8', async () => {
    const many = Array.from({ length: 8 }, (_, i) => ({
      id: `backup-${i + 1}`,
      model: 'model-b',
      criteria: '',
    }))
    serve(fallbackWire({ candidates: many }))
    await openEditor()
    expect(screen.getByRole('button', { name: 'Add backup model' })).toBeDisabled()
    expect(screen.getByText('Backup models (8/8)')).toBeInTheDocument()
  })
})

describe('fallback save', () => {
  it('PUTs the edited order and no decision fields', async () => {
    serve(fallbackWire())
    put.mockResolvedValue(fallbackWire())
    await openEditor()
    await click('Move backup 2 up')
    await click('Save')
    await waitFor(() => expect(put).toHaveBeenCalled())
    expect(put.mock.calls[0][0]).toBe('/routing-policies/model-a')
    expect(put.mock.calls[0][1]).toEqual({
      strategy: 'fallback',
      enabled: false,
      candidates: [
        { id: 'backup-2', model: 'model-c', criteria: '' },
        { id: 'backup-1', model: 'model-b', criteria: '' },
      ],
    })
  })

  it('switching a stored decision policy to fallback sends the fallback shape', async () => {
    serve(decisionWire())
    put.mockResolvedValue(fallbackWire())
    await openEditor()
    await click('Fallback', 'radio')
    await click('Add backup model')
    await userEvent.selectOptions(
      screen.getByLabelText('Backup 1 model'),
      'model-b'
    )
    await click('Save')
    await waitFor(() => expect(put).toHaveBeenCalled())
    expect(put.mock.calls[0][1]).toEqual({
      strategy: 'fallback',
      enabled: false,
      candidates: [{ id: 'backup-1', model: 'model-b', criteria: '' }],
    })
  })

  it('switching a stored fallback policy back to decision sends the decision shape', async () => {
    serve(fallbackWire())
    put.mockResolvedValue(decisionWire())
    await openEditor()
    await click('Decision routing', 'radio')
    fireEvent.change(screen.getByLabelText('Evaluator model'), {
      target: { value: 'model-e' },
    })
    fireEvent.change(screen.getByLabelText('Candidate 1 criteria'), {
      target: { value: 'simple' },
    })
    fireEvent.change(screen.getByLabelText('Candidate 1 id'), {
      target: { value: 'c1' },
    })
    await click('Save')
    await waitFor(() => expect(put).toHaveBeenCalled())
    expect(put.mock.calls[0][1]).toEqual({
      strategy: 'decision',
      enabled: false,
      evaluator_model: 'model-e',
      instructions: '',
      min_confidence: 0.65,
      default_candidate: '',
      candidates: [{ id: 'c1', model: 'model-a', criteria: 'simple' }],
    })
  })

  it('blocks saving a fallback with no backup', async () => {
    serve(null)
    await openEditor()
    await click('Fallback', 'radio')
    await click('Save')
    expect(
      await screen.findByText(
        'A fallback policy needs between 1 and 8 backup models.'
      )
    ).toBeInTheDocument()
    expect(put).not.toHaveBeenCalled()
  })

  it('blocks a backup with no model picked', async () => {
    serve(null)
    await openEditor()
    await click('Fallback', 'radio')
    await click('Add backup model')
    await click('Save')
    expect(
      await screen.findByText('Backup 1: choose a model (up to 128 bytes).')
    ).toBeInTheDocument()
    expect(put).not.toHaveBeenCalled()
  })
})

describe('fallback confirm-before-enable', () => {
  it('enabling asks first, with the fallback wording', async () => {
    serve(fallbackWire())
    put.mockResolvedValue(fallbackWire({ enabled: true }))
    await openEditor()
    await click('Enabled', 'switch')
    await click('Save')
    expect(put).not.toHaveBeenCalled()
    expect(
      await screen.findByText('Enable fallback routing?')
    ).toBeInTheDocument()
    await userEvent.click(await screen.findByRole('button', { name: 'Enable' }))
    await waitFor(() =>
      expect(put.mock.calls[0][1]).toMatchObject({
        strategy: 'fallback',
        enabled: true,
      })
    )
  })

  it('changing the strategy of an already-enabled policy asks first', async () => {
    serve(decisionWire({ enabled: true }))
    put.mockResolvedValue(fallbackWire({ enabled: true }))
    await openEditor()
    await click('Fallback', 'radio')
    await click('Add backup model')
    await userEvent.selectOptions(
      screen.getByLabelText('Backup 1 model'),
      'model-b'
    )
    await click('Save')
    expect(put).not.toHaveBeenCalled()
    expect(
      await screen.findByText('Enable fallback routing?')
    ).toBeInTheDocument()
  })

  it('re-saving an enabled policy with the same strategy does not ask again', async () => {
    serve(fallbackWire({ enabled: true }))
    put.mockResolvedValue(fallbackWire({ enabled: true }))
    await openEditor()
    await click('Save')
    await waitFor(() => expect(put).toHaveBeenCalled())
  })
})
