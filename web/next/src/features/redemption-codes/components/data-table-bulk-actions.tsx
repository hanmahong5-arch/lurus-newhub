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
import type { Table } from '@tanstack/react-table'
import { Trash2 } from 'lucide-react'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { ConfirmDialog } from '@/components/confirm-dialog'
import { DataTableBulkActions as BulkActionsToolbar } from '@/components/data-table'
import { Button } from '@/components/ui/button'
import {
  Tooltip,
  TooltipContent,
  TooltipTrigger,
} from '@/components/ui/tooltip'

import { deleteRedemption } from '../api'
import { deleteMany } from '../lib/bulk-delete'
import type { Redemption } from '../types'
import { useRedemptions } from './redemptions-provider'

interface DataTableBulkActionsProps {
  table: Table<Redemption>
}

export function DataTableBulkActions({ table }: DataTableBulkActionsProps) {
  const { t } = useTranslation()
  const { refresh } = useRedemptions()
  const [targets, setTargets] = useState<Redemption[] | null>(null)
  const selectedRows = table.getFilteredSelectedRowModel().rows

  const deletion = useMutation({
    mutationFn: (list: Redemption[]) =>
      deleteMany(
        list.map((code) => code.id),
        deleteRedemption
      ),
    onSuccess: (result, list) => {
      if (result.failed.length === 0) {
        toast.success(
          t('Successfully deleted {{count}} redemption codes', {
            count: result.deleted.length,
          })
        )
      } else {
        toast.error(
          t('Deleted {{done}} of {{total}} redemption codes', {
            done: result.deleted.length,
            total: list.length,
          }),
          { description: result.failed[0].message }
        )
      }
      // Only the codes that are really gone leave the selection.
      table.setRowSelection((previous) => {
        const next = { ...previous }
        for (const id of result.deleted) delete next[String(id)]
        return next
      })
      setTargets(null)
      refresh()
    },
    onError: () => {
      toast.error(
        t('Failed to delete {{count}} redemption codes', {
          count: targets?.length ?? 0,
        })
      )
    },
  })

  return (
    <>
      <BulkActionsToolbar table={table} entityName={t('redemption code')}>
        <Tooltip>
          <TooltipTrigger
            render={
              <Button
                variant='destructive'
                size='icon'
                className='size-8'
                aria-label={t('Delete selected redemption codes')}
                disabled={deletion.isPending}
                onClick={() =>
                  setTargets(selectedRows.map((row) => row.original))
                }
              />
            }
          >
            <Trash2 aria-hidden='true' />
          </TooltipTrigger>
          <TooltipContent>
            {t('Delete selected redemption codes')}
          </TooltipContent>
        </Tooltip>
      </BulkActionsToolbar>
      <ConfirmDialog
        destructive
        open={targets !== null}
        onOpenChange={(open) => {
          if (!open && !deletion.isPending) setTargets(null)
        }}
        title={t('Delete {{count}} redemption codes?', {
          count: targets?.length ?? 0,
        })}
        desc={t('This action cannot be undone.')}
        confirmText={deletion.isPending ? t('Deleting...') : t('Delete')}
        isLoading={deletion.isPending}
        disabled={!targets?.length}
        handleConfirm={() => {
          if (targets?.length && !deletion.isPending) {
            deletion.mutate(targets)
          }
        }}
      />
    </>
  )
}
