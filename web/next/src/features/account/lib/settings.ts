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

import {
  NOTIFY_TYPES,
  type NotifyType,
  type UpdateUserSettingBody,
  type UserSettings,
} from '../types'

/** The stored setting is a JSON string; anything unreadable counts as empty. */
export function parseUserSettings(
  raw: string | undefined | null
): UserSettings {
  if (!raw) return {}
  try {
    const parsed: unknown = JSON.parse(raw)
    return typeof parsed === 'object' && parsed !== null
      ? (parsed as UserSettings)
      : {}
  } catch {
    return {}
  }
}

export function isNotifyType(value: unknown): value is NotifyType {
  return (NOTIFY_TYPES as readonly string[]).includes(String(value))
}

/** Editable copy of the stored preferences (all text fields are strings). */
export interface SettingsForm {
  notifyType: NotifyType
  /** Threshold in display money (see useQuota.toDisplayAmount), as typed. */
  threshold: string
  notificationEmail: string
  webhookUrl: string
  webhookSecret: string
  barkUrl: string
  gotifyUrl: string
  gotifyToken: string
  gotifyPriority: string
  acceptUnpriced: boolean
  recordIpLog: boolean
}

/**
 * `toDisplay` converts the stored quota threshold into display money; it
 * returns null while the money config is unknown, which leaves the field
 * empty instead of showing a guessed amount.
 */
export function formFromSettings(
  settings: UserSettings,
  toDisplay: (quota: number) => number | null
): SettingsForm {
  const quota = settings.quota_warning_threshold
  const display =
    typeof quota === 'number' && quota > 0 ? toDisplay(quota) : null
  return {
    notifyType: isNotifyType(settings.notify_type)
      ? settings.notify_type
      : 'email',
    threshold: display === null ? '' : String(display),
    notificationEmail: settings.notification_email ?? '',
    webhookUrl: settings.webhook_url ?? '',
    webhookSecret: settings.webhook_secret ?? '',
    barkUrl: settings.bark_url ?? '',
    gotifyUrl: settings.gotify_url ?? '',
    gotifyToken: settings.gotify_token ?? '',
    gotifyPriority:
      typeof settings.gotify_priority === 'number' &&
      settings.gotify_priority > 0
        ? String(settings.gotify_priority)
        : '',
    acceptUnpriced: settings.accept_unset_model_ratio_model === true,
    recordIpLog: settings.record_ip_log === true,
  }
}

function isHttpUrl(value: string): boolean {
  try {
    const url = new URL(value)
    return url.protocol === 'http:' || url.protocol === 'https:'
  } catch {
    return false
  }
}

export type SettingsCheck =
  { ok: true; body: UpdateUserSettingBody } | { ok: false; message: string }

/**
 * Mirror of handler.UpdateUserSetting's validation so the person sees the
 * reason before the round trip. `fromDisplay` converts the typed threshold
 * back to quota units (null while the money config is unknown).
 */
export function checkSettings(
  form: SettingsForm,
  fromDisplay: (amount: number) => number | null,
  t: TFunction
): SettingsCheck {
  const typed = Number(form.threshold)
  if (form.threshold.trim() === '' || !Number.isFinite(typed) || typed <= 0) {
    return { ok: false, message: t('The warning threshold must be above 0.') }
  }
  const quota = fromDisplay(typed)
  if (quota === null || quota <= 0) {
    return {
      ok: false,
      message: t('The warning threshold must be above 0.'),
    }
  }

  const body: UpdateUserSettingBody = {
    notify_type: form.notifyType,
    quota_warning_threshold: quota,
    accept_unset_model_ratio_model: form.acceptUnpriced,
    record_ip_log: form.recordIpLog,
  }

  switch (form.notifyType) {
    case 'email': {
      const email = form.notificationEmail.trim()
      if (email !== '' && !email.includes('@')) {
        return { ok: false, message: t('Enter a valid email address.') }
      }
      if (email !== '') body.notification_email = email
      break
    }
    case 'webhook': {
      const url = form.webhookUrl.trim()
      if (url === '') {
        return { ok: false, message: t('The webhook URL is required.') }
      }
      if (!isHttpUrl(url)) {
        return { ok: false, message: t('Enter a valid webhook URL.') }
      }
      body.webhook_url = url
      // The server clears the stored secret unless it is sent again.
      if (form.webhookSecret !== '') body.webhook_secret = form.webhookSecret
      break
    }
    case 'bark': {
      const url = form.barkUrl.trim()
      if (url === '') {
        return { ok: false, message: t('The Bark push URL is required.') }
      }
      if (!isHttpUrl(url)) {
        return {
          ok: false,
          message: t('The Bark push URL must start with http:// or https://.'),
        }
      }
      body.bark_url = url
      break
    }
    case 'gotify': {
      const url = form.gotifyUrl.trim()
      if (url === '') {
        return { ok: false, message: t('The Gotify server URL is required.') }
      }
      if (!isHttpUrl(url)) {
        return {
          ok: false,
          message: t(
            'The Gotify server URL must start with http:// or https://.'
          ),
        }
      }
      if (form.gotifyToken.trim() === '') {
        return { ok: false, message: t('The Gotify token is required.') }
      }
      body.gotify_url = url
      body.gotify_token = form.gotifyToken.trim()
      const priority = Number(form.gotifyPriority)
      body.gotify_priority =
        form.gotifyPriority.trim() !== '' &&
        Number.isInteger(priority) &&
        priority >= 0 &&
        priority <= 10
          ? priority
          : 5
      break
    }
  }
  return { ok: true, body }
}

/** True when two forms would send the same values. */
export function sameForm(a: SettingsForm, b: SettingsForm): boolean {
  return (Object.keys(a) as Array<keyof SettingsForm>).every(
    (key) => a[key] === b[key]
  )
}
