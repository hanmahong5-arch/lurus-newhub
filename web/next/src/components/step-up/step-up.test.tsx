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
import { AxiosError } from 'axios'
import { useState } from 'react'
import { beforeEach, describe, expect, it, vi } from 'vitest'

import { ApiError } from '@/lib/api'

import { isStepUpRequired, stepUpCodeOf } from './api'
import { StepUpCancelledError, useStepUp } from './use-step-up'

const { get, post } = vi.hoisted(() => ({ get: vi.fn(), post: vi.fn() }))

vi.mock('@/lib/api', async (orig) => {
  const actual = await orig<typeof import('@/lib/api')>()
  return {
    ...actual,
    api: { get, post, put: vi.fn(), delete: vi.fn() },
  }
})

function gate(code = 'VERIFICATION_REQUIRED') {
  return new ApiError('need verification', { status: 403, code })
}

describe('stepUpCodeOf / isStepUpRequired', () => {
  it('reads the code off the wrapped axios error body', () => {
    const cause = new AxiosError('x', 'ERR_BAD_REQUEST', undefined, undefined, {
      status: 403,
      statusText: 'Forbidden',
      headers: {},
      config: {} as never,
      data: { success: false, code: 'VERIFICATION_EXPIRED' },
    })
    const err = new ApiError('expired', { status: 403, cause })
    expect(stepUpCodeOf(err)).toBe('VERIFICATION_EXPIRED')
    expect(isStepUpRequired(err)).toBe(true)
  })

  it('does not treat other 403s or non-API errors as a step-up challenge', () => {
    expect(isStepUpRequired(new ApiError('no', { status: 403 }))).toBe(false)
    expect(
      isStepUpRequired(
        new ApiError('no', { status: 400, code: 'VERIFICATION_REQUIRED' })
      )
    ).toBe(false)
    expect(isStepUpRequired(new Error('boom'))).toBe(false)
  })
})

function Harness(props: { action: () => Promise<string> }) {
  const { run, dialog } = useStepUp()
  const [out, setOut] = useState('idle')
  return (
    <div>
      <button
        type='button'
        onClick={() =>
          run(props.action).then(
            (v) => setOut(`ok:${v}`),
            (e: unknown) =>
              setOut(
                e instanceof StepUpCancelledError
                  ? 'cancelled'
                  : `err:${(e as Error).message}`
              )
          )
        }
      >
        go
      </button>
      <output>{out}</output>
      {dialog}
    </div>
  )
}

function renderHarness(action: () => Promise<string>) {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  return render(
    <QueryClientProvider client={client}>
      <Harness action={action} />
    </QueryClientProvider>
  )
}

describe('useStepUp', () => {
  beforeEach(() => {
    // jsdom has no elementFromPoint; the OTP input probes it on a timer.
    document.elementFromPoint = () => null
    get.mockReset()
    post.mockReset()
  })

  it('passes a successful action straight through without a prompt', async () => {
    const action = vi.fn().mockResolvedValue('v')
    renderHarness(action)
    await userEvent.click(screen.getByText('go'))
    await waitFor(() => expect(screen.getByRole('status')).toHaveTextContent('ok:v'))
    expect(action).toHaveBeenCalledTimes(1)
    expect(get).not.toHaveBeenCalled()
  })

  it('rethrows an unrelated failure untouched', async () => {
    const action = vi.fn().mockRejectedValue(new ApiError('disk full'))
    renderHarness(action)
    await userEvent.click(screen.getByText('go'))
    await waitFor(() =>
      expect(screen.getByRole('status')).toHaveTextContent('err:disk full')
    )
    expect(get).not.toHaveBeenCalled()
  })

  it('verifies with a TOTP code then retries the action once', async () => {
    const action = vi
      .fn()
      .mockRejectedValueOnce(gate())
      .mockResolvedValueOnce('secret')
    get.mockResolvedValue({
      verified: false,
      totp_enrolled: true,
      enrollment_required: false,
    })
    post.mockResolvedValue({ verified: true, expires_at: 1, totp_enrolled: true })
    renderHarness(action)
    await userEvent.click(screen.getByText('go'))
    const input = await screen.findByLabelText('Verification code')
    await userEvent.type(input, '123456')
    await userEvent.click(screen.getByRole('button', { name: 'Verify' }))
    await waitFor(() =>
      expect(screen.getByRole('status')).toHaveTextContent('ok:secret')
    )
    expect(post).toHaveBeenCalledWith('/api/verify', {
      method: 'totp',
      code: '123456',
    })
    expect(action).toHaveBeenCalledTimes(2)
  })

  it('shows the server message on a wrong code and keeps the prompt open', async () => {
    const action = vi.fn().mockRejectedValue(gate('VERIFICATION_EXPIRED'))
    get.mockResolvedValue({
      verified: false,
      totp_enrolled: true,
      enrollment_required: false,
    })
    post.mockRejectedValue(new ApiError('wrong code', { status: 403 }))
    renderHarness(action)
    await userEvent.click(screen.getByText('go'))
    await userEvent.type(await screen.findByLabelText('Verification code'), '000000')
    await userEvent.click(screen.getByRole('button', { name: 'Verify' }))
    expect(await screen.findByText('wrong code')).toBeInTheDocument()
    expect(screen.getByRole('status', { hidden: true })).toHaveTextContent(
      'idle'
    )
    expect(action).toHaveBeenCalledTimes(1)
  })

  it('verifies an unenrolled account by session and retries', async () => {
    const action = vi
      .fn()
      .mockRejectedValueOnce(gate())
      .mockResolvedValueOnce('done')
    get.mockResolvedValue({
      verified: false,
      totp_enrolled: false,
      enrollment_required: false,
    })
    post.mockResolvedValue({ verified: true, expires_at: 1, totp_enrolled: false })
    renderHarness(action)
    await userEvent.click(screen.getByText('go'))
    await waitFor(() =>
      expect(screen.getByRole('status')).toHaveTextContent('ok:done')
    )
    expect(post).toHaveBeenCalledWith('/api/verify', {
      method: 'session',
      code: undefined,
    })
  })

  it('tells an unenrolled account to enrol when the policy requires it', async () => {
    const action = vi.fn().mockRejectedValue(gate())
    get.mockResolvedValue({
      verified: false,
      totp_enrolled: false,
      enrollment_required: true,
    })
    renderHarness(action)
    await userEvent.click(screen.getByText('go'))
    expect(
      await screen.findByText(/Two-factor authentication must be set up/)
    ).toBeInTheDocument()
    expect(post).not.toHaveBeenCalled()
  })

  it('rejects with StepUpCancelledError when the prompt is cancelled', async () => {
    const action = vi.fn().mockRejectedValue(gate())
    get.mockResolvedValue({
      verified: false,
      totp_enrolled: true,
      enrollment_required: false,
    })
    renderHarness(action)
    await userEvent.click(screen.getByText('go'))
    await userEvent.click(await screen.findByRole('button', { name: 'Cancel' }))
    await waitFor(() =>
      expect(screen.getByRole('status')).toHaveTextContent('cancelled')
    )
    expect(action).toHaveBeenCalledTimes(1)
  })
})
