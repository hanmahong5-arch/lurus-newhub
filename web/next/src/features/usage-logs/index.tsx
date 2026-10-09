import { useQuery } from '@tanstack/react-query'
import { Inbox } from 'lucide-react'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'

import { EmptyState } from '@/components/empty-state'
import { ErrorState } from '@/components/error-state'
import { PageHeader } from '@/components/layout/page-header'
import { LoadingState } from '@/components/loading-state'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Checkbox } from '@/components/ui/checkbox'
import { Input } from '@/components/ui/input'
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table'
import { ROLE } from '@/lib/roles'
import { currentLocale } from '@/lib/locale'
import { formatQuota } from '@/lib/money'
import { useMoneyConfig } from '@/lib/status'
import { currentUserQueryOptions } from '@/lib/user'

import { fetchLogStat, fetchLogs } from './api'
import { LogDetailSheet } from './components/log-detail-sheet'
import { StatHeader } from './components/stat-header'
import {
  EMPTY_FILTERS,
  PAGE_SIZE,
  formatLogTime,
  isErrorLog,
  isSettlementFailed,
  latencyMs,
  normalizeList,
  pageCount,
  type LogFilters,
  type UsageLog,
} from './lib/logs'

function Field(props: { label: string; children: React.ReactNode }) {
  return (
    <label className='flex min-w-40 flex-1 flex-col gap-1 text-xs'>
      <span className='text-muted-foreground'>{props.label}</span>
      {props.children}
    </label>
  )
}

export function UsageLogsPage() {
  const { t } = useTranslation()
  const money = useMoneyConfig()
  const { data: user } = useQuery(currentUserQueryOptions)
  const isAdmin = (user?.role ?? 0) >= ROLE.ADMIN

  // `draft` is what the inputs show; `applied` is what the queries ran with.
  const [draft, setDraft] = useState<LogFilters>(EMPTY_FILTERS)
  const [applied, setApplied] = useState<LogFilters>(EMPTY_FILTERS)
  const [page, setPage] = useState(1)
  const [selected, setSelected] = useState<UsageLog | null>(null)

  // The tenant-wide toggle is meaningless (and 403s) for non-admins.
  const effective: LogFilters = {
    ...applied,
    tenantWide: isAdmin && applied.tenantWide,
  }

  const list = useQuery({
    queryKey: ['usage-logs', 'list', effective, page],
    queryFn: () => fetchLogs(effective, page),
    retry: false,
  })
  const stat = useQuery({
    queryKey: ['usage-logs', 'stat', effective],
    queryFn: () => fetchLogStat(effective),
    retry: false,
  })

  const set = <K extends keyof LogFilters>(key: K, value: LogFilters[K]) =>
    setDraft((d) => ({ ...d, [key]: value }))

  const apply = () => {
    setApplied(draft)
    setPage(1)
  }
  const reset = () => {
    setDraft(EMPTY_FILTERS)
    setApplied(EMPTY_FILTERS)
    setPage(1)
  }

  const { logs, total } = normalizeList(list.data)
  const pages = pageCount(total)

  let body: React.ReactNode
  if (list.isPending) {
    body = <LoadingState />
  } else if (list.isError) {
    // A failed read is an error, never an empty list.
    body = (
      <ErrorState
        title={t('Failed to load logs')}
        description={list.error.message}
        onRetry={() => void list.refetch()}
      />
    )
  } else if (logs.length === 0) {
    body = (
      <EmptyState icon={Inbox} title={t('No logs in this window')} bordered />
    )
  } else {
    body = (
      <div className='rounded-lg border'>
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>{t('Time')}</TableHead>
              <TableHead>{t('Model')}</TableHead>
              <TableHead>{t('Token')}</TableHead>
              {effective.tenantWide && <TableHead>{t('User')}</TableHead>}
              <TableHead className='text-right'>
                {t('Tokens in / out')}
              </TableHead>
              <TableHead className='text-right'>{t('Cost')}</TableHead>
              <TableHead className='text-right'>{t('Duration')}</TableHead>
              <TableHead>{t('Status')}</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {logs.map((row) => {
              const latency = latencyMs(row)
              return (
                <TableRow
                  key={row.id}
                  data-testid='log-row'
                  className='cursor-pointer'
                  onClick={() => setSelected(row)}
                >
                  <TableCell className='whitespace-nowrap'>
                    {formatLogTime(row.created_at)}
                  </TableCell>
                  <TableCell>{row.model_name || '--'}</TableCell>
                  <TableCell>{row.token_name || '--'}</TableCell>
                  {effective.tenantWide && (
                    <TableCell>{row.username || '--'}</TableCell>
                  )}
                  <TableCell className='text-right tabular-nums'>
                    {row.prompt_tokens} / {row.completion_tokens}
                  </TableCell>
                  <TableCell className='text-right tabular-nums'>
                    <div>{formatQuota(row.quota, money, 4)}</div>
                  </TableCell>
                  <TableCell className='text-right tabular-nums'>
                    {latency !== null ? `${latency}ms` : '--'}
                  </TableCell>
                  <TableCell>
                    <div className='flex flex-wrap gap-1'>
                      <Badge
                        variant={isErrorLog(row) ? 'destructive' : 'secondary'}
                      >
                        {isErrorLog(row) ? t('Error') : t('OK')}
                      </Badge>
                      {isSettlementFailed(row) && (
                        <Badge variant='warning'>
                          {t('Settlement failed')}
                        </Badge>
                      )}
                    </div>
                  </TableCell>
                </TableRow>
              )
            })}
          </TableBody>
        </Table>
      </div>
    )
  }

  return (
    <>
      <PageHeader title={t('Usage Logs')} />
      <StatHeader
        stat={stat.data}
        loading={stat.isPending}
        failed={stat.isError}
        money={money}
      />

      <form
        className='flex flex-wrap items-end gap-3'
        onSubmit={(e) => {
          e.preventDefault()
          apply()
        }}
      >
        <Field label={t('Start time')}>
          <Input
            type='datetime-local'
            aria-label={t('Start time')}
            value={draft.start}
            onChange={(e) => set('start', e.target.value)}
          />
        </Field>
        <Field label={t('End time')}>
          <Input
            type='datetime-local'
            aria-label={t('End time')}
            value={draft.end}
            onChange={(e) => set('end', e.target.value)}
          />
        </Field>
        <Field label={t('Model')}>
          <Input
            aria-label={t('Model')}
            value={draft.model}
            onChange={(e) => set('model', e.target.value)}
          />
        </Field>
        <Field label={t('Token')}>
          <Input
            aria-label={t('Token')}
            value={draft.token}
            onChange={(e) => set('token', e.target.value)}
          />
        </Field>
        <Field label={t('Request ID')}>
          <Input
            aria-label={t('Request ID')}
            value={draft.requestId}
            onChange={(e) => set('requestId', e.target.value)}
          />
        </Field>
        <label className='flex items-center gap-2 pb-1.5 text-sm'>
          <Checkbox
            checked={draft.errorsOnly}
            onCheckedChange={(v) => set('errorsOnly', v === true)}
          />
          {t('Errors only')}
        </label>
        {isAdmin && (
          <label className='flex items-center gap-2 pb-1.5 text-sm'>
            <Checkbox
              checked={draft.tenantWide}
              onCheckedChange={(v) => set('tenantWide', v === true)}
            />
            {t('All users')}
          </label>
        )}
        <div className='flex gap-2'>
          <Button type='submit'>{t('Search')}</Button>
          <Button type='button' variant='outline' onClick={reset}>
            {t('Reset')}
          </Button>
        </div>
      </form>

      {body}

      {list.isSuccess && total > 0 && (
        <div className='flex items-center justify-between text-sm'>
          <span className='text-muted-foreground'>
            {t('Total')} {total.toLocaleString(currentLocale())} · {PAGE_SIZE} {t('per page')}
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
            <span className='tabular-nums'>
              {page} / {pages}
            </span>
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
      )}

      <LogDetailSheet
        log={selected}
        money={money}
        tenantWide={effective.tenantWide}
        onClose={() => setSelected(null)}
      />
    </>
  )
}
