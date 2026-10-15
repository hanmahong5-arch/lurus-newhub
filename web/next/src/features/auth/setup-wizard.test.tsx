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
import { act, render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { SetupWizard } from './setup-wizard'

const { httpGet, httpPost, assign, reload, toastError, toastSuccess } =
  vi.hoisted(() => ({
    httpGet: vi.fn(),
    httpPost: vi.fn(),
    assign: vi.fn(),
    reload: vi.fn(),
    toastError: vi.fn(),
    toastSuccess: vi.fn(),
  }))

vi.mock('sonner', () => ({
  toast: { error: toastError, success: toastSuccess },
}))

vi.mock('@/lib/api', async (orig) => {
  const actual = await orig<typeof import('@/lib/api')>()
  return { ...actual, http: { get: httpGet, post: httpPost } }
})

vi.mock('./lib/navigate', () => ({
  navigation: { replace: vi.fn(), assign, reload },
}))

vi.mock('./components/auth-layout', () => ({
  AuthLayout: (props: { children: React.ReactNode }) => (
    <div data-testid='layout'>{props.children}</div>
  ),
}))

function setupOk(over: Record<string, unknown> = {}) {
  return {
    data: {
      success: true,
      data: {
        status: false,
        root_init: false,
        database_type: 'postgres',
        ...over,
      },
    },
  }
}

const currentStep = () =>
  screen.getByTestId('setup-steps').getAttribute('data-current')

async function mount(over: Record<string, unknown> = {}) {
  httpGet.mockResolvedValue(setupOk(over))
  const utils = render(<SetupWizard />)
  await screen.findByTestId('setup-wizard')
  return utils
}

function click(user: ReturnType<typeof userEvent.setup>, name: string) {
  return user.click(screen.getByRole('button', { name }))
}

beforeEach(() => {
  httpPost.mockResolvedValue({ data: { success: true } })
})

afterEach(() => {
  vi.useRealTimers()
})

describe('SetupWizard: bootstrap', () => {
  it('starts on the database step with all four steps listed', async () => {
    await mount()
    expect(currentStep()).toBe('0')
    expect(httpGet).toHaveBeenCalledWith('/api/setup', {
      skipAuthRedirect: true,
    })
    expect(within(screen.getByTestId('setup-steps')).getAllByRole('listitem')).toHaveLength(4)
    expect(screen.getByTestId('setup-db-postgres')).toBeInTheDocument()
  })

  it('leaves the wizard entirely when setup has already been completed', async () => {
    httpGet.mockResolvedValue(setupOk({ status: true }))
    render(<SetupWizard />)
    await waitFor(() => expect(assign).toHaveBeenCalledWith('/'))
    expect(screen.queryByTestId('setup-wizard')).not.toBeInTheDocument()
  })

  it('reports a refused status call instead of pretending the system is fresh', async () => {
    httpGet.mockResolvedValue({ data: { success: false } })
    render(<SetupWizard />)
    expect(await screen.findByTestId('setup-load-failed')).toBeInTheDocument()
    expect(toastError).toHaveBeenCalledWith('Failed to get initialization status')
    expect(assign).not.toHaveBeenCalled()
    expect(screen.queryByTestId('setup-wizard')).not.toBeInTheDocument()
  })

  it('reports a failed status call and can retry it', async () => {
    httpGet.mockRejectedValueOnce(new Error('offline'))
    const user = userEvent.setup()
    render(<SetupWizard />)
    expect(await screen.findByTestId('setup-load-failed')).toBeInTheDocument()
    expect(toastError).toHaveBeenCalledWith('Failed to get initialization status')

    httpGet.mockResolvedValue(setupOk())
    await click(user, 'Retry')
    expect(await screen.findByTestId('setup-wizard')).toBeInTheDocument()
  })
})

describe('SetupWizard: database step', () => {
  it('warns about SQLite persistence', async () => {
    await mount({ database_type: 'sqlite' })
    const warning = screen.getByTestId('setup-db-sqlite')
    expect(warning).toHaveTextContent('Database warning')
    expect(warning.textContent).toContain('all data is lost')
  })

  it('says nothing about a database the server did not name', async () => {
    await mount({ database_type: '' })
    expect(screen.queryByTestId('setup-db-sqlite')).not.toBeInTheDocument()
    expect(screen.queryByTestId('setup-db-postgres')).not.toBeInTheDocument()
    expect(screen.getByTestId('setup-db-unknown')).toBeInTheDocument()
  })
})

describe('SetupWizard: step gating', () => {
  it('lets the database step through unconditionally', async () => {
    const user = userEvent.setup()
    await mount()
    await click(user, 'Next step')
    expect(currentStep()).toBe('1')
    expect(toastError).not.toHaveBeenCalled()
  })

  it('refuses to leave the admin step without a username', async () => {
    const user = userEvent.setup()
    await mount()
    await click(user, 'Next step')
    await click(user, 'Next step')
    expect(toastError).toHaveBeenCalledWith('Please enter the admin username')
    expect(currentStep()).toBe('1')
    expect(screen.getByTestId('setup-username')).toHaveAttribute('aria-invalid', 'true')
  })

  it('treats a blank username as missing', async () => {
    const user = userEvent.setup()
    await mount()
    await click(user, 'Next step')
    await user.type(screen.getByTestId('setup-username'), '   ')
    await click(user, 'Next step')
    expect(toastError).toHaveBeenCalledWith('Please enter the admin username')
    expect(currentStep()).toBe('1')
  })

  it('refuses a username longer than the server accepts', async () => {
    const user = userEvent.setup()
    await mount()
    await click(user, 'Next step')
    await user.type(screen.getByTestId('setup-username'), 'abcdefghijklm')
    await click(user, 'Next step')
    expect(toastError).toHaveBeenCalledWith(
      'The username must be 1-12 characters long'
    )
    expect(currentStep()).toBe('1')
  })

  it('accepts a well-formed username and clears the invalid mark when editing', async () => {
    const user = userEvent.setup()
    await mount()
    await click(user, 'Next step')
    await click(user, 'Next step')
    await user.type(screen.getByTestId('setup-username'), 'root')
    expect(screen.getByTestId('setup-username')).not.toHaveAttribute('aria-invalid')
    await click(user, 'Next step')
    expect(currentStep()).toBe('2')
  })

  it('never asks for a password: credentials live on the identity platform', async () => {
    const user = userEvent.setup()
    await mount()
    await click(user, 'Next step')
    expect(screen.queryByLabelText(/password/i)).not.toBeInTheDocument()
    expect(document.querySelector('input[type="password"]')).toBeNull()
  })

  it('skips the username entirely when root is already initialised', async () => {
    const user = userEvent.setup()
    await mount({ root_init: true })
    await click(user, 'Next step')
    expect(screen.getByTestId('setup-root-initialized')).toBeInTheDocument()
    expect(screen.queryByTestId('setup-username')).not.toBeInTheDocument()
    await click(user, 'Next step')
    expect(toastError).not.toHaveBeenCalled()
    expect(currentStep()).toBe('2')
  })

  it('defaults to the external mode and marks the chosen one', async () => {
    const user = userEvent.setup()
    await mount({ root_init: true })
    await click(user, 'Next step')
    await click(user, 'Next step')
    expect(screen.getByTestId('setup-mode-external')).toHaveAttribute('aria-checked', 'true')
    await user.click(screen.getByTestId('setup-mode-self'))
    expect(screen.getByTestId('setup-mode-self')).toHaveAttribute('aria-checked', 'true')
    expect(screen.getByTestId('setup-mode-external')).toHaveAttribute('aria-checked', 'false')
  })

  it('lists each mode feature set, marking excluded features', async () => {
    const user = userEvent.setup()
    await mount({ root_init: true })
    await click(user, 'Next step')
    await click(user, 'Next step')
    const self = screen.getByTestId('setup-mode-self')
    // "self" mode advertises two things it does NOT do: those must not be
    // dressed up as included features.
    expect(
      within(self).getByText('No user registration').closest('[data-included]')
    ).toHaveAttribute('data-included', 'false')
    expect(
      within(self).getByText('Unified API key').closest('[data-included]')
    ).toHaveAttribute('data-included', 'true')
  })

  it('carries the chosen mode into the confirmation summary', async () => {
    const user = userEvent.setup()
    await mount({ root_init: true })
    await click(user, 'Next step')
    await click(user, 'Next step')
    await user.click(screen.getByTestId('setup-mode-self'))
    await click(user, 'Next step')
    expect(currentStep()).toBe('3')
    expect(
      screen.getByTestId('setup-summary').querySelector('[data-summary-key="mode"]')
    ).toHaveTextContent('Self-use mode')
  })

  it('walks back a step without losing the entered name', async () => {
    const user = userEvent.setup()
    await mount()
    await click(user, 'Next step')
    await user.type(screen.getByTestId('setup-username'), 'root')
    await click(user, 'Next step')
    expect(currentStep()).toBe('2')
    await click(user, 'Previous step')
    expect(currentStep()).toBe('1')
    expect(screen.getByTestId('setup-username')).toHaveValue('root')
    await click(user, 'Next step')
    expect(currentStep()).toBe('2')
    expect(toastError).not.toHaveBeenCalled()
  })

  it('offers no way back from the first step', async () => {
    await mount()
    expect(screen.queryByRole('button', { name: 'Previous step' })).not.toBeInTheDocument()
  })
})

describe('SetupWizard: confirmation summary', () => {
  const summary = (key: string) =>
    screen.getByTestId('setup-summary').querySelector(`[data-summary-key="${key}"]`)

  async function toLastStep(user: ReturnType<typeof userEvent.setup>) {
    await click(user, 'Next step')
    await click(user, 'Next step')
    await click(user, 'Next step')
  }

  it.each([
    ['sqlite', 'SQLite'],
    ['postgres', 'PostgreSQL'],
  ])('reports database_type=%s as %s', async (type, label) => {
    const user = userEvent.setup()
    await mount({ root_init: true, database_type: type })
    await toLastStep(user)
    expect(summary('database')).toHaveTextContent(label)
  })

  it('never states a database it was not told about', async () => {
    const user = userEvent.setup()
    await mount({ root_init: true, database_type: '' })
    await toLastStep(user)
    expect(summary('database')).toHaveTextContent('Unknown')
    expect(summary('database')).not.toHaveTextContent('PostgreSQL')
  })

  it.each([
    ['setup-mode-external', 'Default mode'],
    ['setup-mode-self', 'Self-use mode'],
    ['setup-mode-demo', 'Demo site mode'],
  ])('reports %s as %s', async (testId, label) => {
    const user = userEvent.setup()
    await mount({ root_init: true })
    await click(user, 'Next step')
    await click(user, 'Next step')
    await user.click(screen.getByTestId(testId))
    await click(user, 'Next step')
    expect(summary('mode')).toHaveTextContent(label)
  })

  it('shows the chosen admin name, or "Initialized" when root exists', async () => {
    const user = userEvent.setup()
    const first = await mount()
    await click(user, 'Next step')
    await user.type(screen.getByTestId('setup-username'), 'operator')
    await click(user, 'Next step')
    await click(user, 'Next step')
    expect(summary('admin')).toHaveTextContent('operator')
    first.unmount()

    await mount({ root_init: true })
    await toLastStep(user)
    expect(summary('admin')).toHaveTextContent('Initialized')
  })
})

describe('SetupWizard: submission', () => {
  async function reachFinalStep(
    user: ReturnType<typeof userEvent.setup>,
    over: Record<string, unknown> = {}
  ) {
    await mount({ root_init: true, ...over })
    await click(user, 'Next step')
    await click(user, 'Next step')
    await click(user, 'Next step')
    expect(currentStep()).toBe('3')
  }

  it('translates the usage mode into the two backend flags and drops the UI-only field', async () => {
    const user = userEvent.setup()
    await mount({ root_init: true })
    await click(user, 'Next step')
    await click(user, 'Next step')
    await user.click(screen.getByTestId('setup-mode-demo'))
    await click(user, 'Next step')
    await click(user, 'Initialize system')

    await waitFor(() => expect(httpPost).toHaveBeenCalledTimes(1))
    const [url, body] = httpPost.mock.calls[0]
    expect(url).toBe('/api/setup')
    expect(body.SelfUseModeEnabled).toBe(false)
    expect(body.DemoSiteEnabled).toBe(true)
    expect(body).not.toHaveProperty('usageMode')
  })

  it('marks self-use mode without also marking demo mode', async () => {
    const user = userEvent.setup()
    await mount({ root_init: true })
    await click(user, 'Next step')
    await click(user, 'Next step')
    await user.click(screen.getByTestId('setup-mode-self'))
    await click(user, 'Next step')
    await click(user, 'Initialize system')
    await waitFor(() => expect(httpPost).toHaveBeenCalledTimes(1))
    expect(httpPost.mock.calls[0][1]).toMatchObject({
      SelfUseModeEnabled: true,
      DemoSiteEnabled: false,
    })
  })

  it('submits the administrator name on a fresh install and nothing secret', async () => {
    const user = userEvent.setup()
    await mount()
    await click(user, 'Next step')
    await user.type(screen.getByTestId('setup-username'), ' root ')
    await click(user, 'Next step')
    await click(user, 'Next step')
    await click(user, 'Initialize system')
    await waitFor(() => expect(httpPost).toHaveBeenCalledTimes(1))
    expect(httpPost.mock.calls[0][1]).toEqual({
      username: 'root',
      SelfUseModeEnabled: false,
      DemoSiteEnabled: false,
    })
  })

  it('sends no username when root already exists', async () => {
    const user = userEvent.setup()
    await reachFinalStep(user)
    await click(user, 'Initialize system')
    await waitFor(() => expect(httpPost).toHaveBeenCalledTimes(1))
    expect(httpPost.mock.calls[0][1]).not.toHaveProperty('username')
  })

  it('reloads into the initialised system after a successful submit', async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true })
    const user = userEvent.setup({ advanceTimers: vi.advanceTimersByTime })
    await reachFinalStep(user)
    await click(user, 'Initialize system')

    await waitFor(() =>
      expect(toastSuccess).toHaveBeenCalledWith(
        'System initialized, redirecting...'
      )
    )
    expect(reload).not.toHaveBeenCalled()
    await act(async () => {
      vi.advanceTimersByTime(1500)
    })
    expect(reload).toHaveBeenCalledTimes(1)
  })

  it('surfaces the backend refusal message and does not reload', async () => {
    httpPost.mockResolvedValue({
      data: { success: false, message: 'database is read-only' },
    })
    const user = userEvent.setup()
    await reachFinalStep(user)
    await click(user, 'Initialize system')
    await waitFor(() =>
      expect(toastError).toHaveBeenCalledWith('database is read-only')
    )
    expect(toastSuccess).not.toHaveBeenCalled()
    expect(reload).not.toHaveBeenCalled()
  })

  it('falls back to a generic message when the refusal carries none', async () => {
    httpPost.mockResolvedValue({ data: { success: false } })
    const user = userEvent.setup()
    await reachFinalStep(user)
    await click(user, 'Initialize system')
    await waitFor(() =>
      expect(toastError).toHaveBeenCalledWith('Initialization failed, please retry')
    )
  })

  it('reports a transport failure and re-arms the button for a retry', async () => {
    httpPost.mockRejectedValueOnce(new Error('boom'))
    const user = userEvent.setup()
    await reachFinalStep(user)
    await click(user, 'Initialize system')
    await waitFor(() =>
      expect(toastError).toHaveBeenCalledWith(
        'System initialization failed, please try again'
      )
    )
    const button = screen.getByRole('button', { name: 'Initialize system' })
    await waitFor(() => expect(button).toBeEnabled())

    await user.click(button)
    await waitFor(() => expect(httpPost).toHaveBeenCalledTimes(2))
  })

  // The legacy wizard re-armed its button the moment the request finished, so
  // for the 1.5s before the reload a second click fired a second POST: a
  // second attempt to create the root administrator.
  it('a successful initialisation does not accept a second submit before the reload', async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true })
    const user = userEvent.setup({ advanceTimers: vi.advanceTimersByTime })
    await reachFinalStep(user)
    await click(user, 'Initialize system')
    await waitFor(() => expect(toastSuccess).toHaveBeenCalled())

    const button = screen.getByRole('button', { name: 'Initialize system' })
    expect(button).toBeDisabled()
    await user.click(button)
    expect(httpPost).toHaveBeenCalledTimes(1)

    await act(async () => {
      vi.advanceTimersByTime(1500)
    })
    expect(reload).toHaveBeenCalledTimes(1)
    // A run the backend accepted is never reported as a failure.
    expect(toastError).not.toHaveBeenCalled()
  })

  it('does not take a second click while the first request is in flight', async () => {
    let finish: (v: unknown) => void = () => {}
    httpPost.mockReturnValue(
      new Promise((res) => {
        finish = res
      })
    )
    const user = userEvent.setup()
    await reachFinalStep(user)
    await click(user, 'Initialize system')
    const button = screen.getByRole('button', { name: 'Initialize system' })
    await waitFor(() => expect(button).toBeDisabled())
    await user.click(button)
    expect(httpPost).toHaveBeenCalledTimes(1)
    await act(async () => {
      finish({ data: { success: false, message: 'no' } })
    })
  })
})
