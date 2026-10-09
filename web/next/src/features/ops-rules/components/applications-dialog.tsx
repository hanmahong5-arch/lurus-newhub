import { useQuery } from '@tanstack/react-query'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'

import { ErrorState } from '@/components/error-state'
import { LoadingState } from '@/components/loading-state'
import { Badge } from '@/components/ui/badge'
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

import { APPLICATIONS_PAGE_SIZE, applicationsQueryOptions } from '../api'
import type { ChannelTemplate } from '../types'

function formatTime(sec: number): string {
  return sec > 0 ? new Date(sec * 1000).toLocaleString() : '--'
}

/** Where this template was written, read from the server's append-only log. */
export function ApplicationsDialog(props: {
  template: ChannelTemplate
  onClose: () => void
}) {
  const { t } = useTranslation()
  const { template } = props
  const [page, setPage] = useState(1)
  const q = useQuery(applicationsQueryOptions(template.id, page))
  const rows = q.data?.applications ?? []
  const total = q.data?.total ?? 0
  const pages = Math.max(1, Math.ceil(total / APPLICATIONS_PAGE_SIZE))

  let body
  if (q.isPending) {
    body = <LoadingState />
  } else if (q.isError) {
    // A failed read is an error, never "not applied anywhere".
    body = (
      <ErrorState
        title={t('Could not load the application history')}
        description={toApiError(q.error).message}
        onRetry={() => void q.refetch()}
      />
    )
  } else if (rows.length === 0) {
    body = (
      <p
        className='text-muted-foreground text-sm'
        data-testid='applications-empty'
      >
        {t('This template has not been applied to any channel yet.')}
      </p>
    )
  } else {
    body = (
      <div className='grid gap-2' data-testid='applications-table'>
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>{t('Channel')}</TableHead>
              <TableHead>{t('Version')}</TableHead>
              <TableHead>{t('Applied by')}</TableHead>
              <TableHead>{t('Applied at')}</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {rows.map((a) => (
              <TableRow key={a.id}>
                <TableCell>#{a.channel_id}</TableCell>
                <TableCell>
                  <Badge
                    variant={
                      a.template_version === template.version
                        ? 'default'
                        : 'secondary'
                    }
                  >
                    v{a.template_version}
                  </Badge>
                </TableCell>
                <TableCell>
                  {a.applied_by > 0 ? `#${a.applied_by}` : '--'}
                </TableCell>
                <TableCell>{formatTime(a.applied_at)}</TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
        <div className='flex items-center justify-between text-sm'>
          <span className='text-muted-foreground'>
            {t('{{count}} applications in total', { count: total })}
          </span>
          <div className='flex items-center gap-2'>
            <Button
              variant='outline'
              size='sm'
              disabled={page <= 1}
              onClick={() => setPage((p) => Math.max(1, p - 1))}
            >
              {t('Previous')}
            </Button>
            <span>{t('Page {{page}} of {{pages}}', { page, pages })}</span>
            <Button
              variant='outline'
              size='sm'
              disabled={page >= pages}
              onClick={() => setPage((p) => p + 1)}
            >
              {t('Next')}
            </Button>
          </div>
        </div>
      </div>
    )
  }

  return (
    <Dialog open onOpenChange={(open) => !open && props.onClose()}>
      <DialogContent className='sm:max-w-2xl'>
        <DialogHeader>
          <DialogTitle>
            {t('Application history of {{name}}', { name: template.name })}
          </DialogTitle>
          <DialogDescription>
            {t(
              'Channels this template was written into, newest first. Older versions need re-applying.'
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
