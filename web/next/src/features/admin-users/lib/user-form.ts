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
import { z } from 'zod'

import { NAME_MAX_LENGTH, REMARK_MAX_LENGTH } from '../constants'
import type { AdminUser, UserEditPatch } from '../types'

export function getUserFormSchema(t: TFunction) {
  return z.object({
    username: z.string(),
    display_name: z
      .string()
      .max(
        NAME_MAX_LENGTH,
        t('Display name can be at most {{max}} characters', {
          max: NAME_MAX_LENGTH,
        })
      ),
    group: z.string().min(1, t('Select a group')),
    remark: z
      .string()
      .max(
        REMARK_MAX_LENGTH,
        t('Remark can be at most {{max}} characters', {
          max: REMARK_MAX_LENGTH,
        })
      ),
  })
}

export type UserFormValues = z.infer<ReturnType<typeof getUserFormSchema>>

export const USER_FORM_DEFAULTS: UserFormValues = {
  username: '',
  display_name: '',
  group: 'default',
  remark: '',
}

export function userToFormValues(user: AdminUser): UserFormValues {
  return {
    username: user.username,
    display_name: user.display_name ?? '',
    group: user.group || 'default',
    remark: user.remark ?? '',
  }
}

/**
 * Edit patch from the form. A blank display name falls back to the username,
 * as the login flow shows it; quota is never part of this patch (it is
 * changed by Adjust Quota, which reads the live balance).
 */
export function formValuesToPatch(values: UserFormValues): UserEditPatch {
  return {
    display_name: values.display_name.trim() || values.username,
    group: values.group,
    remark: values.remark.trim(),
  }
}
