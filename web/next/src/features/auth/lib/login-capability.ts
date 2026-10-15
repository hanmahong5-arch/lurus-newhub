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
import { api } from '@/lib/api'

export interface LoginCapability {
  /** Single sign-on is wired on this instance. */
  sso: boolean
  /** The acceptance bridge route is registered on this instance. */
  bridge: boolean
}

interface LoginMethods {
  sso?: { enabled?: boolean }
  bridge?: { enabled?: boolean }
}

/**
 * What this deployment can actually sign someone in with.
 *
 * `login_methods.oidc` answers a different question (the legacy OAuth
 * settings block) and reads false even where sign-on works, so it is not
 * used. `login_methods.sso` is the login handler's own nil check.
 *
 * Only a definite "no" changes the destination: an unreachable /api/status,
 * or a payload with no sso entry at all, still redirects. That is the path
 * this screen has always taken and the one that reports its own failure, so
 * an absent flag must not be read as "cannot sign in". The bridge flag is the
 * opposite: absent means do not offer it, because offering a sign-in form for
 * a route that is not registered collects a token and 404s.
 */
export async function loginCapability(): Promise<LoginCapability> {
  try {
    const status = await api.get<{ login_methods?: LoginMethods }>(
      '/api/status',
      { skipAuthRedirect: true }
    )
    const methods = status?.login_methods ?? {}
    return {
      sso: methods.sso?.enabled !== false,
      bridge: methods.bridge?.enabled === true,
    }
  } catch {
    return { sso: true, bridge: false }
  }
}

/** Whether the instance has the bridge route; an unreachable status says yes. */
export async function bridgeOffered(): Promise<boolean> {
  try {
    const status = await api.get<{ login_methods?: LoginMethods }>(
      '/api/status',
      { skipAuthRedirect: true }
    )
    return status?.login_methods?.bridge?.enabled === true
  } catch {
    // Status unreachable. Let the exchange itself answer rather than refusing
    // on a guess.
    return true
  }
}
