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
import { useTranslation } from 'react-i18next'

import { CopyButton } from '@/components/copy-button'
import { Button } from '@/components/ui/button'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'

interface KeySecretDialogProps {
  secret: string
  rotated: boolean
  onShowSnippets: () => void
  onClose: () => void
}

/** The one place the plaintext key is ever displayed. */
export function KeySecretDialog(props: KeySecretDialogProps) {
  const { t } = useTranslation()
  return (
    <Dialog
      open
      onOpenChange={(open) => {
        if (!open) props.onClose()
      }}
    >
      <DialogContent className='sm:max-w-lg'>
        <DialogHeader>
          <DialogTitle>
            {props.rotated ? t('Key rotated') : t('Key created')}
          </DialogTitle>
          <DialogDescription>
            {props.rotated
              ? t(
                  'The old key no longer works. Copy the new key now; it will not be shown again after you leave this page.'
                )
              : t(
                  'Copy the key now; it will not be shown again after you leave this page.'
                )}
          </DialogDescription>
        </DialogHeader>
        <div className='flex items-center gap-2 rounded-md border p-2'>
          <code
            data-testid='issued-key'
            className='min-w-0 flex-1 font-mono text-sm break-all'
          >
            {props.secret}
          </code>
          <CopyButton value={props.secret} aria-label={t('Copy key')} />
        </div>
        <DialogFooter>
          <Button variant='outline' onClick={props.onShowSnippets}>
            {t('Usage examples')}
          </Button>
          <Button onClick={props.onClose}>{t('Done')}</Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
