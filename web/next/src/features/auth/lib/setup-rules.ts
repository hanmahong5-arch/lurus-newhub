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

/** GET /api/setup (handler.GetSetup). */
export interface SetupStatus {
  /** Setup has already been completed. */
  status: boolean
  /** A root user exists, so no administrator has to be created. */
  root_init: boolean
  /** "postgres", or "sqlite" in the hermetic test tier; empty until known. */
  database_type: string
}

export type UsageMode = 'external' | 'self' | 'demo'

/** SetupRequest (handler/setup.go) minus the fields the server never reads. */
export interface SetupPayload {
  username?: string
  SelfUseModeEnabled: boolean
  DemoSiteEnabled: boolean
}

/** Mirrors the server's 1-12 character rule (counted in characters, not bytes). */
export const USERNAME_MAX_CHARS = 12

export type UsernameProblem = 'required' | 'too-long'

/**
 * The administrator username rule. Accounts, passwords and registration live
 * on the identity platform, so setup asks for a name only: the server creates
 * the root row from `username` and ignores everything else.
 */
export function usernameProblem(raw: string): UsernameProblem | null {
  const name = raw.trim()
  if (name === '') return 'required'
  if ([...name].length > USERNAME_MAX_CHARS) return 'too-long'
  return null
}

/**
 * The submit body. `usageMode` is UI-only: the backend stores two flags, and
 * "external" is simply both off.
 */
export function buildSetupPayload(input: {
  rootInit: boolean
  username: string
  usageMode: UsageMode
}): SetupPayload {
  const payload: SetupPayload = {
    SelfUseModeEnabled: input.usageMode === 'self',
    DemoSiteEnabled: input.usageMode === 'demo',
  }
  if (!input.rootInit) payload.username = input.username.trim()
  return payload
}
