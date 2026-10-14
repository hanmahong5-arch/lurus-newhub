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
import { AlertTriangle } from 'lucide-react'
import { useTranslation } from 'react-i18next'

import { ConfirmDialog } from '@/components/confirm-dialog'

type PassthroughWarningDialogProps = {
  open: boolean
  onOpenChange: (open: boolean) => void
  handleConfirm: () => void
}

/**
 * Shown before request-body passthrough is switched on. Passthrough bypasses
 * most of the gateway's request processing, so the user must acknowledge which
 * features stop working first.
 */
export function PassthroughWarningDialog(props: PassthroughWarningDialogProps) {
  const { t } = useTranslation()
  const effects = [
    t('Model redirect is unavailable'),
    t('Parameter override and system prompt are unavailable'),
    t(
      'Format conversion is unavailable; the client must use a format the upstream supports'
    ),
  ]

  return (
    <ConfirmDialog
      open={props.open}
      onOpenChange={props.onOpenChange}
      title={
        <span className='text-destructive flex items-center gap-2'>
          <AlertTriangle className='size-5 shrink-0' aria-hidden='true' />
          {t('Enable request body passthrough?')}
        </span>
      }
      desc={
        <div className='space-y-3 text-sm'>
          <p>
            {t(
              'Enable when the client and upstream use the same API format and you need to preserve upstream-specific or new fields the gateway does not yet support. Usually leave this off.'
            )}
          </p>
          <p>
            {t(
              'After enabling, the original request body is forwarded to the upstream without gateway rewriting:'
            )}
          </p>
          <ul className='list-disc space-y-1 pl-5'>
            {effects.map((item) => (
              <li key={item}>{item}</li>
            ))}
          </ul>
          <p className='text-foreground font-medium'>
            {t('Make sure you understand these effects before enabling.')}
          </p>
        </div>
      }
      confirmText={t('Enable passthrough')}
      destructive
      handleConfirm={props.handleConfirm}
    />
  )
}
