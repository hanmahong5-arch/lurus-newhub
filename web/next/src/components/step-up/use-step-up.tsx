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
import { useCallback, useRef, useState, type ReactNode } from 'react'

import { isStepUpRequired } from './api'
import { StepUpDialog } from './step-up-dialog'

/** Thrown by `run` when the person closes the verification prompt. */
export class StepUpCancelledError extends Error {
  constructor() {
    super('Verification cancelled')
    this.name = 'StepUpCancelledError'
  }
}

type Pending = {
  promise: Promise<void>
  resolve: () => void
  reject: (error: unknown) => void
}

/**
 * Run a request that may sit behind the server's step-up gate.
 *
 * `run(action)` calls `action`; if the server answers 403
 * VERIFICATION_REQUIRED / EXPIRED / INVALID it opens the verification prompt,
 * and after the person verifies it calls `action` once more. Any other error
 * (and a second step-up refusal) is rethrown untouched. Render `dialog`
 * somewhere in the tree. Cancelling rejects with StepUpCancelledError.
 *
 *   const { run, dialog } = useStepUp()
 *   const reveal = () => run(() => api.post(url))
 */
export function useStepUp(): {
  run: <T>(action: () => Promise<T>) => Promise<T>
  dialog: ReactNode
} {
  const [open, setOpen] = useState(false)
  const pending = useRef<Pending | null>(null)

  const challenge = useCallback((): Promise<void> => {
    if (pending.current) return pending.current.promise
    let resolve!: () => void
    let reject!: (error: unknown) => void
    const promise = new Promise<void>((res, rej) => {
      resolve = res
      reject = rej
    })
    pending.current = { promise, resolve, reject }
    setOpen(true)
    return promise
  }, [])

  const settle = useCallback((ok: boolean) => {
    const current = pending.current
    pending.current = null
    setOpen(false)
    if (!current) return
    if (ok) current.resolve()
    else current.reject(new StepUpCancelledError())
  }, [])

  const run = useCallback(
    async <T,>(action: () => Promise<T>): Promise<T> => {
      try {
        return await action()
      } catch (error) {
        if (!isStepUpRequired(error)) throw error
        await challenge()
        return action()
      }
    },
    [challenge]
  )

  const dialog = (
    <StepUpDialog
      open={open}
      onVerified={() => settle(true)}
      onCancel={() => settle(false)}
    />
  )

  return { run, dialog }
}
