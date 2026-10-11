import { keepPreviousData, useMutation, useQuery } from '@tanstack/react-query'
import { Download, Inbox } from 'lucide-react'
import { useState, type ReactNode } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { EmptyState } from '@/components/empty-state'
import { ErrorState } from '@/components/error-state'
import { LoadingState } from '@/components/loading-state'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table'
import { toApiError } from '@/lib/api'
import { formatDateTimeObject } from '@/lib/time'

import { auditQueryOptions, exportAuditCsv } from '../api'
import {
  EMPTY_AUDIT_FILTERS,
  auditFilterError,
  pageCount,
  type AuditFilters,
} from '../lib/policy'

function Field(props: { label: string; children: ReactNode }) {
  return (
    <label className='flex min-w-36 flex-1 flex-col gap-1 text-xs'>
      <span className='text-muted-foreground'>{props.label}</span>
      {props.children}
    </label>
  )
}

/** Hand the CSV text to the browser as a file download. */
function download(csv: string) {
  // BOM so spreadsheet apps read the UTF-8 text correctly.
  const blob = new Blob([String.fromCharCode(0xfeff), csv], { type: 'text/csv;charset=utf-8' })
  const url = URL.createObjectURL(blob)
  const a = document.createElement('a')
  a.href = url
  a.download = 'audit-events.csv'
  document.body.appendChild(a)
  a.click()
  a.remove()
  URL.revokeObjectURL(url)
}

export function AuditSection() {
  const { t } = useTranslation()
  const [draft, setDraft] = useState<AuditFilters>(EMPTY_AUDIT_FILTERS)
  const [applied, setApplied] = useState<AuditFilters>(EMPTY_AUDIT_FILTERS)
  const [page, setPage] = useState(1)
  const [formError, setFormError] = useState<string | null>(null)

  const list = useQuery({
    ...auditQueryOptions(applied, page),
    placeholderData: keepPreviousData,
  })

  const exporter = useMutation({
    mutationFn: () => exportAuditCsv(applied),
    onSuccess: ({ csv, truncated }) => {
      download(csv)
      if (truncated) {
        toast.warning(
          t('The file holds the first part of the matching events only. Narrow the filters to export the rest.')
        )
      }
    },
    onError: (e) => toast.error(toApiError(e).message),
  })

  const set = <K extends keyof AuditFilters>(k: K, v: AuditFilters[K]) => {
    setDraft((d) => ({ ...d, [k]: v }))
    setFormError(null)
  }

  const apply = (next: AuditFilters = draft) => {
    const problem = auditFilterError(next)
    if (problem === 'actor') {
      setFormError(t('The operator must be a positive whole number (user id).'))
      return
    }
    if (problem === 'range') {
      setFormError(t('The start date is after the end date.'))
      return
    }
    setDraft(next)
    setApplied(next)
    setPage(1)
  }

  const reset = () => {
    setFormError(null)
    setDraft(EMPTY_AUDIT_FILTERS)
    setApplied(EMPTY_AUDIT_FILTERS)
    setPage(1)
  }

  let body: ReactNode
  if (list.isPending) {
    body = <LoadingState />
  } else if (list.isError) {
    // A failed read is an error, never an empty list.
    body = (
      <ErrorState
        title={t('Failed to load the audit log')}
        description={list.error.message}
        onRetry={() => void list.refetch()}
      />
    )
  } else if (list.data.items.length === 0) {
    body = <EmptyState icon={Inbox} title={t('No audit events match')} bordered />
  } else {
    const pages = pageCount(list.data.total)
    body = (
      <div className='space-y-3'>
        <div className='overflow-x-auto rounded-lg border'>
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>{t('Time')}</TableHead>
                <TableHead>{t('Action')}</TableHead>
                <TableHead>{t('Operator')}</TableHead>
                <TableHead>{t('Resource')}</TableHead>
                <TableHead>{t('IP')}</TableHead>
                <TableHead>{t('Details')}</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {list.data.items.map((e) => (
                <TableRow key={e.id} data-testid='audit-row'>
                  <TableCell className='whitespace-nowrap'>
                    {e.timestamp > 0
                      ? formatDateTimeObject(new Date(e.timestamp * 1000))
                      : '--'}
                  </TableCell>
                  <TableCell className='font-mono text-xs'>{e.action}</TableCell>
                  <TableCell className='whitespace-nowrap'>
                    {e.actorType}
                    {e.actorId > 0 ? ` #${e.actorId}` : ''}
                  </TableCell>
                  <TableCell className='whitespace-nowrap'>
                    {e.resource}
                    {e.resourceId > 0 ? ` #${e.resourceId}` : ''}
                  </TableCell>
                  <TableCell className='font-mono text-xs'>{e.ip || '--'}</TableCell>
                  <TableCell
                    className='max-w-72 truncate font-mono text-xs'
                    title={e.details}
                  >
                    {e.details || '--'}
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        </div>
        <div className='flex items-center justify-between gap-2 text-sm'>
          <span className='text-muted-foreground'>
            {t('{{total}} events, page {{page}} of {{pages}}', {
              total: list.data.total,
              page,
              pages,
            })}
          </span>
          <div className='flex gap-2'>
            <Button
              variant='outline'
              size='sm'
              disabled={page <= 1 || list.isFetching}
              onClick={() => setPage((p) => p - 1)}
            >
              {t('Previous')}
            </Button>
            <Button
              variant='outline'
              size='sm'
              disabled={page >= pages || list.isFetching}
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
    <div className='space-y-4'>
      <form
        className='flex flex-wrap items-end gap-3'
        onSubmit={(e) => {
          e.preventDefault()
          apply()
        }}
      >
        <Field label={t('Action')}>
          <Input
            value={draft.action}
            onChange={(e) => set('action', e.target.value)}
            placeholder='data_policy.rule_hit'
            aria-label={t('Action')}
            className='font-mono'
          />
        </Field>
        <Field label={t('Operator (user id)')}>
          <Input
            inputMode='numeric'
            value={draft.actorId}
            onChange={(e) => set('actorId', e.target.value)}
            aria-label={t('Operator (user id)')}
          />
        </Field>
        <Field label={t('From')}>
          <Input
            type='date'
            value={draft.from}
            onChange={(e) => set('from', e.target.value)}
            aria-label={t('From')}
          />
        </Field>
        <Field label={t('To')}>
          <Input
            type='date'
            value={draft.to}
            onChange={(e) => set('to', e.target.value)}
            aria-label={t('To')}
          />
        </Field>
        <div className='flex flex-wrap gap-2'>
          <Button type='submit'>{t('Search logs')}</Button>
          <Button type='button' variant='outline' onClick={reset}>
            {t('Reset')}
          </Button>
          <Button
            type='button'
            variant='outline'
            onClick={() => apply({ ...draft, action: 'data_policy.rule_hit' })}
          >
            {t('Rule matches')}
          </Button>
          <Button
            type='button'
            variant='outline'
            disabled={exporter.isPending}
            onClick={() => exporter.mutate()}
          >
            <Download />
            {exporter.isPending ? t('Exporting...') : t('Export CSV')}
          </Button>
        </div>
      </form>
      <p className='text-muted-foreground text-xs'>
        {t('Export uses the filters last applied with Search logs.')}
      </p>
      {formError !== null && (
        <p role='alert' className='text-destructive text-sm'>
          {formError}
        </p>
      )}
      {body}
    </div>
  )
}
