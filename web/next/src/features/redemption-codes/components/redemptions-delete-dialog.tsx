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
import { useMutation } from '@tanstack/react-query'
import { Trans, useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { ConfirmDialog } from '@/components/confirm-dialog'
import { handleServerError } from '@/lib/handle-server-error'

import { deleteInvalidRedemptions, deleteRedemption } from '../api'
import type { Redemption } from '../types'
import { useRedemptions } from './redemptions-provider'

interface RedemptionsDeleteDialogProps {
  redemption: Redemption | null
  /** True when the invalid-code sweep is the open dialog. */
  invalidOpen: boolean
  /** A platform operator's sweep covers every tenant, not just this one. */
  global: boolean
}

export function RedemptionsDeleteDialog({
  redemption,
  invalidOpen,
  global,
}: RedemptionsDeleteDialogProps) {
  const { t } = useTranslation()
  const { setDialog, refresh } = useRedemptions()

  const remove = useMutation({
    mutationFn: (id: number) => deleteRedemption(id),
    onSuccess: () => {
      toast.success(t('Redemption code deleted successfully'))
      setDialog(null)
      refresh()
    },
    onError: (error) =>
      handleServerError(error, t('Failed to delete redemption code')),
  })

  const sweep = useMutation({
    mutationFn: () => deleteInvalidRedemptions(),
    onSuccess: (count) => {
      toast.success(
        t('Successfully deleted {{count}} invalid redemption codes', {
          count: count ?? 0,
        })
      )
      setDialog(null)
      refresh()
    },
    onError: (error) =>
      handleServerError(
        error,
        t('Failed to delete invalid redemption codes')
      ),
  })

  return (
    <>
      <ConfirmDialog
        destructive
        open={redemption !== null}
        onOpenChange={(open) => {
          if (!open && !remove.isPending) setDialog(null)
        }}
        title={t('Are you sure?')}
        desc={
          <>
            {t('This will permanently delete redemption code')}{' '}
            <span className='font-semibold'>{redemption?.name}</span>
            {t('. This action cannot be undone.')}
          </>
        }
        confirmText={remove.isPending ? t('Deleting...') : t('Delete')}
        isLoading={remove.isPending}
        handleConfirm={() => {
          if (redemption && !remove.isPending) remove.mutate(redemption.id)
        }}
      />

      <ConfirmDialog
        destructive
        open={invalidOpen}
        onOpenChange={(open) => {
          if (!open && !sweep.isPending) setDialog(null)
        }}
        className='max-w-md'
        title={t('Delete Invalid Redemption Codes?')}
        desc={
          <>
            <Trans
              t={t}
              i18nKey='This will delete all <strong>used</strong>, <strong>disabled</strong>, and <strong>expired</strong> redemption codes.'
              components={{ strong: <strong /> }}
            />
            <br />
            {global &&
              t('As a platform operator this covers every tenant, not only yours.')}
            {global && <br />}
            {t('This action cannot be undone.')}
          </>
        }
        confirmText={sweep.isPending ? t('Deleting...') : t('Delete Invalid')}
        isLoading={sweep.isPending}
        handleConfirm={() => {
          if (!sweep.isPending) sweep.mutate()
        }}
      />
    </>
  )
}
