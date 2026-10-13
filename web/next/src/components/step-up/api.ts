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
import axios from 'axios'

import { ApiError, api } from '@/lib/api'

/** Codes middleware.SecureVerificationRequired answers a gated route with. */
export const STEP_UP_CODES = [
  'VERIFICATION_REQUIRED',
  'VERIFICATION_EXPIRED',
  'VERIFICATION_INVALID',
] as const

export type StepUpMethod = 'session' | 'totp' | 'totp_backup'

/** GET /api/verify/status (handler.GetVerificationStatus). */
export interface StepUpStatus {
  verified: boolean
  expires_at?: number
  totp_enrolled: boolean
  /** Process-wide policy: an unenrolled account is refused, not waved through. */
  enrollment_required: boolean
}

/** POST /api/verify (handler.UniversalVerify) success payload. */
export interface StepUpResult {
  verified: boolean
  expires_at: number
  totp_enrolled: boolean
}

/**
 * The gate answers with `{ code }` (not `error_code`), which ApiError.code
 * does not carry; it is still on the wrapped axios error.
 */
export function stepUpCodeOf(error: unknown): string | undefined {
  if (!(error instanceof ApiError)) return undefined
  if (error.code) return error.code
  const cause = error.cause
  if (axios.isAxiosError(cause)) {
    const body: unknown = cause.response?.data
    if (typeof body === 'object' && body !== null) {
      const code = (body as { code?: unknown }).code
      if (typeof code === 'string') return code
    }
  }
  return undefined
}

/** True when a request failed only because the session needs step-up. */
export function isStepUpRequired(error: unknown): boolean {
  const code = stepUpCodeOf(error)
  return (
    error instanceof ApiError &&
    error.status === 403 &&
    code !== undefined &&
    (STEP_UP_CODES as readonly string[]).includes(code)
  )
}

export const stepUpApi = {
  status: () => api.get<StepUpStatus>('/api/verify/status'),
  verify: (method: StepUpMethod, code?: string) =>
    api.post<StepUpResult>('/api/verify', { method, code }),
}
