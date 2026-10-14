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
import { Code2, Pencil, RefreshCw, Trash2 } from 'lucide-react'
import { useTranslation } from 'react-i18next'

import { DataTableRowActionMenu } from '@/components/data-table'
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

import type { ApiToken } from '../types'

export interface KeyRowHandlers {
  onEdit: (token: ApiToken) => void
  onSnippets: (token: ApiToken) => void
  onRotate: (token: ApiToken) => void
  onDelete: (token: ApiToken) => void
}

/** Edit button plus an overflow menu: usage examples, rotate, delete. */
export function KeyRowActions(props: {
  token: ApiToken
  handlers: KeyRowHandlers
}) {
  const { t } = useTranslation()
  const { token, handlers } = props
  return (
    <div className='-ml-1.5 flex items-center gap-1'>
      <Tooltip>
        <TooltipTrigger
          render={
            <Button
              variant='ghost'
              size='icon-sm'
              onClick={() => handlers.onEdit(token)}
              aria-label={t('Edit {{name}}', { name: token.name })}
            />
          }
        >
          <Pencil />
        </TooltipTrigger>
        <TooltipContent>{t('Edit')}</TooltipContent>
      </Tooltip>

      <DataTableRowActionMenu
        ariaLabel={t('More actions for {{name}}', { name: token.name })}
        contentClassName='w-52'
        modal={false}
      >
        <DropdownMenuItem onClick={() => handlers.onSnippets(token)}>
          {t('Usage examples')}
          <DropdownMenuShortcut>
            <Code2 size={16} />
          </DropdownMenuShortcut>
        </DropdownMenuItem>
        <DropdownMenuItem onClick={() => handlers.onRotate(token)}>
          {t('Rotate key')}
          <DropdownMenuShortcut>
            <RefreshCw size={16} />
          </DropdownMenuShortcut>
        </DropdownMenuItem>
        <DropdownMenuSeparator />
        <DropdownMenuItem
          onClick={() => handlers.onDelete(token)}
          className='text-destructive focus:text-destructive'
        >
          {t('Delete key')}
          <DropdownMenuShortcut>
            <Trash2 size={16} />
          </DropdownMenuShortcut>
        </DropdownMenuItem>
      </DataTableRowActionMenu>
    </div>
  )
}
