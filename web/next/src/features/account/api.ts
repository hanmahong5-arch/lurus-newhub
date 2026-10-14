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
import { api, http, tenantApi, tenantUrl } from '@/lib/api'

import type {
  AccountProfile,
  BackupCodes,
  LoginSession,
  SessionList,
  TotpEnrollment,
  TotpStatus,
  UpdateUserSettingBody,
} from './types'

export const accountKeys = {
  profile: ['account', 'profile'] as const,
  totp: ['account', 'totp-status'] as const,
  sessions: ['account', 'sessions'] as const,
}

export function fetchProfile(): Promise<AccountProfile> {
  return tenantApi.get<AccountProfile>('/user/me')
}

/** v1 handler: answers HTTP 200 {success:false} on validation failure. */
export async function saveUserSettings(
  body: UpdateUserSettingBody
): Promise<void> {
  await api.put<unknown>('/api/user/setting', body)
}

export function fetchTotpStatus(): Promise<TotpStatus> {
  return api.get<TotpStatus>('/api/user/totp/status')
}

export function enrollTotp(): Promise<TotpEnrollment> {
  return api.post<TotpEnrollment>('/api/user/totp/enroll')
}

export function confirmTotp(code: string): Promise<BackupCodes> {
  return api.post<BackupCodes>('/api/user/totp/confirm', { code })
}

/** Behind middleware.SecureVerificationRequired: call through useStepUp().run. */
export function regenerateBackupCodes(): Promise<BackupCodes> {
  return api.post<BackupCodes>('/api/user/totp/backup-codes/regenerate')
}

/** Behind middleware.SecureVerificationRequired: call through useStepUp().run. */
export async function disableTotp(): Promise<void> {
  await api.post<unknown>('/api/user/totp/disable')
}

function num(v: unknown): number | undefined {
  return typeof v === 'number' && Number.isFinite(v) ? v : undefined
}

function str(v: unknown): string | undefined {
  return typeof v === 'string' && v !== '' ? v : undefined
}

/** Tolerate a missing list and missing optional fields. */
export function normalizeSessions(raw: unknown): SessionList {
  const r = (raw ?? {}) as Record<string, unknown>
  const items = Array.isArray(r.items) ? r.items : []
  return {
    registry_enabled: r.registry_enabled === true,
    items: items.map((entry): LoginSession => {
      const s = (entry ?? {}) as Record<string, unknown>
      const id =
        typeof s.id === 'number' || typeof s.id === 'string' ? s.id : 'current'
      return {
        id,
        current: s.is_current === true || s.current === true,
        auth_method: str(s.auth_method) ?? '',
        created_at: num(s.created_at),
        last_seen_at: num(s.last_seen_at) ?? num(s.last_seen),
        ip: str(s.ip),
        user_agent_family: str(s.user_agent_family),
      }
    }),
  }
}

export async function fetchSessions(): Promise<SessionList> {
  return normalizeSessions(await tenantApi.get<unknown>('/sessions'))
}

/**
 * DELETE /sessions/:id answers 204 with no envelope, so it cannot go through
 * `api` (which unwraps `{success}`); failures still arrive as ApiError.
 */
export async function revokeSession(id: number): Promise<void> {
  await http.delete(tenantUrl(`/sessions/${id}`))
}

export async function revokeOtherSessions(): Promise<{ revoked: number }> {
  const data = await tenantApi.delete<{ revoked?: number }>('/sessions/others')
  return { revoked: data?.revoked ?? 0 }
}
