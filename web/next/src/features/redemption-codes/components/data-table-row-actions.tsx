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
import { Edit, Power, PowerOff, Trash2 } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { DataTableRowActionMenu } from '@/components/data-table/core/row-action-menu'
import { Button } from '@/components/ui/button'
import {
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuShortcut,
} from '@/components/ui/dropdown-menu'
import {
  Tooltip,
  TooltipContent,
  TooltipTrigger,
} from '@/components/ui/tooltip'
import { handleServerError } from '@/lib/handle-server-error'

import { setRedemptionStatus } from '../api'
import { REDEMPTION_STATUS } from '../constants'
import { isRedemptionExpired } from '../lib/utils'
import type { Redemption } from '../types'
import { useRedemptions } from './redemptions-provider'

interface DataTableRowActionsProps {
  redemption: Redemption
}

export function DataTableRowActions({
  redemption,
}: DataTableRowActionsProps) {
  const { t } = useTranslation()
  const { setDialog, refresh } = useRedemptions()
  const isEnabled = redemption.status === REDEMPTION_STATUS.ENABLED
  const isUsed = redemption.status === REDEMPTION_STATUS.USED
  const isExpired = isRedemptionExpired(redemption)

  const toggle = useMutation({
    mutationFn: () =>
      setRedemptionStatus(
        redemption.id,
        isEnabled ? REDEMPTION_STATUS.DISABLED : REDEMPTION_STATUS.ENABLED
      ),
    onSuccess: () => {
      toast.success(
        isEnabled
          ? t('Redemption code disabled successfully')
          : t('Redemption code enabled successfully')
      )
      refresh()
    },
    onError: (error) =>
      handleServerError(error, t('Failed to update redemption code status')),
  })

  const canEdit = isEnabled && !isExpired
  const canToggle = !isUsed && !isExpired

  return (
    <div className='-ml-1.5 flex items-center gap-1'>
      <Tooltip>
        <TooltipTrigger
          render={
            <Button
              variant='ghost'
              size='icon-sm'
              onClick={() => setDialog({ kind: 'edit', redemption })}
              disabled={!canEdit}
              aria-label={t('Edit')}
            />
          }
        >
          <Edit />
        </TooltipTrigger>
        <TooltipContent>{t('Edit')}</TooltipContent>
      </Tooltip>

      <DataTableRowActionMenu ariaLabel={t('Open menu')} modal={false}>
        {canToggle && (
          <>
            <DropdownMenuItem
              onClick={() => toggle.mutate()}
              disabled={toggle.isPending}
            >
              {isEnabled ? t('Disable') : t('Enable')}
              <DropdownMenuShortcut>
                {isEnabled ? <PowerOff size={16} /> : <Power size={16} />}
              </DropdownMenuShortcut>
            </DropdownMenuItem>
            <DropdownMenuSeparator />
          </>
        )}
        <DropdownMenuItem
          onClick={() => setDialog({ kind: 'delete', redemption })}
          className='text-destructive focus:text-destructive'
        >
          {t('Delete')}
          <DropdownMenuShortcut>
            <Trash2 size={16} />
          </DropdownMenuShortcut>
        </DropdownMenuItem>
      </DataTableRowActionMenu>
    </div>
  )
}
