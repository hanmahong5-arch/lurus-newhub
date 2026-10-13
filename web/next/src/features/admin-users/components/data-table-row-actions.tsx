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
import {
  ArrowDown,
  ArrowUp,
  Coins,
  Pencil,
  Power,
  PowerOff,
  ShieldAlert,
  Trash2,
} from 'lucide-react'
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

import { updateUser } from '../api'
import { USER_ROLE, USER_STATUS, isUserDeleted } from '../constants'
import { canManageRole, useViewer } from '../lib/viewer'
import type { AdminUser, UserEditPatch } from '../types'
import { useUsers } from './users-provider'

interface DataTableRowActionsProps {
  user: AdminUser
}

type QuickAction = 'enable' | 'disable' | 'promote' | 'demote'

const QUICK_PATCH: Record<QuickAction, UserEditPatch> = {
  enable: { status: USER_STATUS.ENABLED },
  disable: { status: USER_STATUS.DISABLED },
  promote: { role: USER_ROLE.ADMIN },
  demote: { role: USER_ROLE.USER },
}

export function DataTableRowActions({ user }: DataTableRowActionsProps) {
  const { t } = useTranslation()
  const { setDialog, setQuotaUser, refresh } = useUsers()
  const viewer = useViewer()

  const quick = useMutation({
    mutationFn: (action: QuickAction) =>
      updateUser(user.id, () => QUICK_PATCH[action]),
    onSuccess: (_next, action) => {
      const messages: Record<QuickAction, string> = {
        enable: t('User enabled'),
        disable: t('User disabled'),
        promote: t('User promoted to admin'),
        demote: t('User demoted to member'),
      }
      toast.success(messages[action])
      refresh()
    },
    onError: (error, action) => {
      const messages: Record<QuickAction, string> = {
        enable: t('Failed to enable user'),
        disable: t('Failed to disable user'),
        promote: t('Failed to promote user'),
        demote: t('Failed to demote user'),
      }
      handleServerError(error, messages[action])
    },
  })

  if (isUserDeleted(user)) return null

  const isSelf = viewer.id === user.id
  const isRootTarget = user.role >= USER_ROLE.ROOT
  const canManage = canManageRole(viewer, user.role) || isSelf
  const isDisabled = user.status === USER_STATUS.DISABLED
  const isAdmin = user.role >= USER_ROLE.ADMIN
  const locked = isSelf || isRootTarget
  const busy = quick.isPending

  return (
    <div className='-ml-1.5 flex items-center gap-1'>
      <Tooltip>
        <TooltipTrigger
          render={
            <Button
              variant='ghost'
              size='icon-sm'
              onClick={() => setDialog({ kind: 'edit', user })}
              disabled={!canManage}
              aria-label={t('Edit')}
            />
          }
        >
          <Pencil />
        </TooltipTrigger>
        <TooltipContent>{t('Edit')}</TooltipContent>
      </Tooltip>

      <DataTableRowActionMenu
        ariaLabel={t('Open menu')}
        contentClassName='w-52'
      >
        <DropdownMenuItem
          onClick={() => setQuotaUser(user)}
          disabled={!canManage}
        >
          {t('Adjust Quota')}
          <DropdownMenuShortcut>
            <Coins size={16} />
          </DropdownMenuShortcut>
        </DropdownMenuItem>

        {isDisabled ? (
          <DropdownMenuItem
            onClick={() => quick.mutate('enable')}
            disabled={!canManage || busy}
          >
            {t('Enable')}
            <DropdownMenuShortcut>
              <Power size={16} />
            </DropdownMenuShortcut>
          </DropdownMenuItem>
        ) : (
          <DropdownMenuItem
            onClick={() => quick.mutate('disable')}
            disabled={!canManage || locked || busy}
          >
            {t('Disable')}
            <DropdownMenuShortcut>
              <PowerOff size={16} />
            </DropdownMenuShortcut>
          </DropdownMenuItem>
        )}

        {isAdmin && !isRootTarget && (
          <DropdownMenuItem
            onClick={() => quick.mutate('demote')}
            disabled={!canManage || locked || busy}
          >
            {t('Demote')}
            <DropdownMenuShortcut>
              <ArrowDown size={16} />
            </DropdownMenuShortcut>
          </DropdownMenuItem>
        )}

        {!isAdmin && (
          <DropdownMenuItem
            onClick={() => quick.mutate('promote')}
            disabled={!canManage || busy}
          >
            {t('Promote')}
            <DropdownMenuShortcut>
              <ArrowUp size={16} />
            </DropdownMenuShortcut>
          </DropdownMenuItem>
        )}

        {viewer.isRoot && (
          <>
            <DropdownMenuSeparator />
            <DropdownMenuItem
              onClick={() => setDialog({ kind: 'force-disable-2fa', user })}
              disabled={isSelf}
            >
              {t('Reset 2FA')}
              <DropdownMenuShortcut>
                <ShieldAlert size={16} />
              </DropdownMenuShortcut>
            </DropdownMenuItem>
            <DropdownMenuItem
              onClick={() => setDialog({ kind: 'delete', user })}
              className='text-destructive focus:text-destructive'
              disabled={locked}
            >
              {t('Delete')}
              <DropdownMenuShortcut>
                <Trash2 size={16} />
              </DropdownMenuShortcut>
            </DropdownMenuItem>
          </>
        )}
      </DataTableRowActionMenu>
    </div>
  )
}
