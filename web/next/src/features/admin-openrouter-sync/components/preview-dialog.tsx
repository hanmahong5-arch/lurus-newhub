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
import { useQuery } from '@tanstack/react-query'
import { useTranslation } from 'react-i18next'

import { EmptyState } from '@/components/empty-state'
import { ErrorState } from '@/components/error-state'
import { LoadingState } from '@/components/loading-state'
import { Button } from '@/components/ui/button'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table'
import { toApiError } from '@/lib/api'
import dayjs from '@/lib/dayjs'

import { previewQueryOptions } from '../api'
import type { SyncJob } from '../types'

/**
 * Dry run of a job: the models that would be imported after filtering and
 * ranking. Nothing is written. A failed dry run is an error, not an empty
 * result.
 */
export function PreviewDialog(props: { job: SyncJob; onClose: () => void }) {
  const { t } = useTranslation()
  const { job } = props
  const preview = useQuery(previewQueryOptions(job.id))
  const items = preview.data ?? []

  let body
  if (preview.isPending) {
    body = <LoadingState />
  } else if (preview.isError) {
    body = (
      <div data-testid='preview-error'>
        <ErrorState
          className='min-h-40'
          title={t('Preview failed')}
          description={toApiError(preview.error).message}
          onRetry={() => void preview.refetch()}
        />
      </div>
    )
  } else if (items.length === 0) {
    body = (
      <EmptyState
        className='min-h-40'
        title={t('No models match')}
        description={t('No model matches the rules of this job right now.')}
      />
    )
  } else {
    body = (
      <div className='max-h-[50dvh] overflow-auto rounded-lg border'>
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>{t('Model ID')}</TableHead>
              <TableHead>{t('Name')}</TableHead>
              <TableHead>{t('Created')}</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {items.map((m) => (
              <TableRow key={m.id}>
                <TableCell className='font-mono text-xs break-all'>
                  {m.id}
                </TableCell>
                <TableCell>{m.name}</TableCell>
                <TableCell className='text-muted-foreground whitespace-nowrap'>
                  {m.created
                    ? dayjs(m.created * 1000).format('YYYY-MM-DD')
                    : '—'}
                </TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      </div>
    )
  }

  return (
    <Dialog
      open
      onOpenChange={(open) => {
        if (!open) props.onClose()
      }}
    >
      <DialogContent className='sm:max-w-2xl'>
        <DialogHeader>
          <DialogTitle>{t('Preview {{name}}', { name: job.name })}</DialogTitle>
          <DialogDescription>
            {t(
              'Models that would be imported after filtering by category and ranking, nothing is written.'
            )}
          </DialogDescription>
        </DialogHeader>
        {body}
        <DialogFooter>
          <Button variant='outline' onClick={props.onClose}>
            {t('Close')}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
