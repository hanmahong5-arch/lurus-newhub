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
import {
  Activity,
  Gauge,
  Pause,
  Pencil,
  Power,
  PowerOff,
  Ticket,
  Wallet,
} from 'lucide-react'
import { useTranslation } from 'react-i18next'

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

import { TENANT_STATUS } from '../constants'
import type { Tenant } from '../types'
import { useTenants } from './tenants-provider'

export function TenantRowActions({ tenant }: { tenant: Tenant }) {
  const { t } = useTranslation()
  const { setDialog } = useTenants()
  const enabled = tenant.status === TENANT_STATUS.ENABLED
  const suspended = tenant.status === TENANT_STATUS.SUSPENDED

  return (
    <div className='-ml-1.5 flex items-center gap-1'>
      <Tooltip>
        <TooltipTrigger
          render={
            <Button
              variant='ghost'
              size='icon-sm'
              onClick={() => setDialog({ kind: 'edit', tenant })}
              aria-label={t('Edit tenant')}
            />
          }
        >
          <Pencil />
        </TooltipTrigger>
        <TooltipContent>{t('Edit tenant')}</TooltipContent>
      </Tooltip>

      <DataTableRowActionMenu ariaLabel={t('Open menu')} modal={false}>
        <DropdownMenuItem
          onClick={() => setDialog({ kind: 'stats', tenant })}
        >
          {t('Stats')}
          <DropdownMenuShortcut>
            <Activity size={16} />
          </DropdownMenuShortcut>
        </DropdownMenuItem>
        <DropdownMenuItem onClick={() => setDialog({ kind: 'pool', tenant })}>
          {t('Credit pool')}
          <DropdownMenuShortcut>
            <Wallet size={16} />
          </DropdownMenuShortcut>
        </DropdownMenuItem>
        <DropdownMenuItem
          onClick={() => setDialog({ kind: 'invites', tenant })}
        >
          {t('Invites')}
          <DropdownMenuShortcut>
            <Ticket size={16} />
          </DropdownMenuShortcut>
        </DropdownMenuItem>
        <DropdownMenuItem
          onClick={() => setDialog({ kind: 'limits', tenant })}
        >
          {t('Rate limits')}
          <DropdownMenuShortcut>
            <Gauge size={16} />
          </DropdownMenuShortcut>
        </DropdownMenuItem>
        <DropdownMenuSeparator />
        {enabled ? (
          <DropdownMenuItem
            onClick={() =>
              setDialog({ kind: 'status', tenant, action: 'disable' })
            }
          >
            {t('Disable tenant')}
            <DropdownMenuShortcut>
              <PowerOff size={16} />
            </DropdownMenuShortcut>
          </DropdownMenuItem>
        ) : (
          <DropdownMenuItem
            onClick={() =>
              setDialog({ kind: 'status', tenant, action: 'enable' })
            }
          >
            {t('Enable tenant')}
            <DropdownMenuShortcut>
              <Power size={16} />
            </DropdownMenuShortcut>
          </DropdownMenuItem>
        )}
        {!suspended && (
          <DropdownMenuItem
            onClick={() =>
              setDialog({ kind: 'status', tenant, action: 'suspend' })
            }
            className='text-warning focus:text-warning'
          >
            {t('Suspend tenant')}
            <DropdownMenuShortcut>
              <Pause size={16} />
            </DropdownMenuShortcut>
          </DropdownMenuItem>
        )}
      </DataTableRowActionMenu>
    </div>
  )
}
