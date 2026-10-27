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
/** GET /api/v2/~/user/me (handler.GetSelfV2) - the fields the account page reads. */
export interface AccountProfile {
  id: number
  username: string
  display_name?: string
  email?: string
  role: number
  status?: number
  group?: string
  quota: number
  used_quota: number
  remaining_quota: number
  request_count?: number
  tenant_slug?: string
  tenant_role?: string
  token_count?: number
  /** JSON document stored on the user row (dto.UserSetting), as a string. */
  setting?: string
}

export const NOTIFY_TYPES = ['email', 'webhook', 'bark', 'gotify'] as const
export type NotifyType = (typeof NOTIFY_TYPES)[number]

/** dto.UserSetting - the stored per-user preferences. */
export interface UserSettings {
  notify_type?: string
  quota_warning_threshold?: number
  webhook_url?: string
  webhook_secret?: string
  notification_email?: string
  bark_url?: string
  gotify_url?: string
  gotify_token?: string
  gotify_priority?: number
  accept_unset_model_ratio_model?: boolean
  record_ip_log?: boolean
}

/** PUT /api/v2/~/user/setting body (handler.UpdateUserSettingRequest). */
export interface UpdateUserSettingBody {
  notify_type: NotifyType
  /** Internal quota units. */
  quota_warning_threshold: number
  webhook_url?: string
  webhook_secret?: string
  notification_email?: string
  bark_url?: string
  gotify_url?: string
  gotify_token?: string
  gotify_priority?: number
  accept_unset_model_ratio_model: boolean
  record_ip_log: boolean
}

/** GET /api/v2/~/user/totp/status. */
export interface TotpStatus {
  enrolled: boolean
  pending: boolean
  backup_codes_remaining: number
}

/** POST /api/v2/~/user/totp/enroll. */
export interface TotpEnrollment {
  secret: string
  otpauth_url: string
}

/** POST /api/v2/~/user/totp/confirm and /backup-codes/regenerate. */
export interface BackupCodes {
  backup_codes: string[]
}

/** One row of GET /api/v2/~/sessions (handler.ListSessionsV2). */
export interface LoginSession {
  /** Registry row id, or the string "current" for the single synthetic row. */
  id: number | string
  current: boolean
  auth_method: string
  /** Unix seconds. */
  created_at?: number
  last_seen_at?: number
  /** Masked by the server (/24 or /48). */
  ip?: string
  /** Coarse browser family, never the raw User-Agent. */
  user_agent_family?: string
}

export interface SessionList {
  items: LoginSession[]
  /** False when the deployment records no per-device sessions at all. */
  registry_enabled: boolean
}
