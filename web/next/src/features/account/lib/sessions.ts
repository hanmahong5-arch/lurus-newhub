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
import type { TFunction } from 'i18next'

import type { LoginSession } from '../types'

/** auth_method as recorded by the server; unknown values are shown verbatim. */
export function authMethodLabel(method: string, t: TFunction): string {
  switch (method.trim().toLowerCase()) {
    case '':
      return t('Unknown')
    case 'session':
      return t('Browser session')
    case 'oidc':
    case 'oidc_jwt':
      return t('Single sign-on')
    default:
      return method
  }
}

export function sessionDeviceLabel(
  session: LoginSession,
  t: TFunction
): string {
  return session.user_agent_family || t('Unknown device')
}

/** Only registered rows (numeric id) can be revoked individually. */
export function revocableId(session: LoginSession): number | null {
  return typeof session.id === 'number' && !session.current ? session.id : null
}
