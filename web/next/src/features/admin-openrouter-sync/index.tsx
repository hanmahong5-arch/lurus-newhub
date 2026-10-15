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
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import {
  CloudDownload,
  Eye,
  Pencil,
  Play,
  Plus,
  RefreshCw,
  Trash2,
  Zap,
} from 'lucide-react'
import { useCallback, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { ConfirmDialog } from '@/components/confirm-dialog'
import { EmptyState } from '@/components/empty-state'
import { ErrorState } from '@/components/error-state'
import { LoadingState } from '@/components/loading-state'
import { SectionPageLayout } from '@/components/section-page-layout'
import { StatusBadge } from '@/components/status-badge'
import { Alert, AlertDescription } from '@/components/ui/alert'
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

import {
  categoriesQueryOptions,
  channelsQueryOptions,
  createJob,
  deleteJob,
  jobsQueryOptions,
  runAllJobs,
  runJob,
  syncQueryKey,
  updateJob,
} from './api'
import { ApiPoolCard } from './components/api-pool-card'
import { JobFormSheet } from './components/job-form-sheet'
import { PreviewDialog } from './components/preview-dialog'
import {
  categoryLabel,
  channelLabel,
  parseCategories,
  scheduleLabel,
} from './lib'
import type { JobBody, RunResult, SyncJob } from './types'

type Dialogs =
  | { kind: 'create' }
  | { kind: 'edit'; job: SyncJob }
  | { kind: 'preview'; job: SyncJob }
  | { kind: 'delete'; job: SyncJob }
  | { kind: 'force'; job: SyncJob }
  | { kind: 'run-all' }
  | { kind: 'breaker' }
  | null

export function AdminOpenRouterSyncPage() {
  const { t } = useTranslation()
  const qc = useQueryClient()
  const [dialog, setDialog] = useState<Dialogs>(null)
  const [saveError, setSaveError] = useState<string | null>(null)
  const [running, setRunning] = useState<ReadonlySet<number>>(new Set())
  const [runningAll, setRunningAll] = useState(false)

  const jobs = useQuery(jobsQueryOptions)
  const categories = useQuery(categoriesQueryOptions)
  const channels = useQuery(channelsQueryOptions)

  const refresh = useCallback(
    () => qc.invalidateQueries({ queryKey: syncQueryKey }),
    [qc]
  )
  const fail = (title: string, e: unknown) =>
    toast.error(title, { description: toApiError(e).message })

  const closeDialog = () => {
    setDialog(null)
    setSaveError(null)
  }

  const save = useMutation({
    mutationFn: (v: { id?: number; body: JobBody }) =>
      v.id ? updateJob(v.id, v.body) : createJob(v.body),
    onSuccess: async () => {
      toast.success(t('Saved'))
      closeDialog()
      await refresh()
    },
    // Stay open: the operator fixes the field the server refused.
    onError: (e) => setSaveError(toApiError(e).message),
  })

  const remove = useMutation({
    mutationFn: (job: SyncJob) => deleteJob(job.id),
    onSuccess: async () => {
      toast.success(t('Deleted'))
      closeDialog()
      await refresh()
    },
    onError: (e) => {
      closeDialog()
      fail(t('Delete failed'), e)
    },
  })

  /** One summary for single-job and run-all results alike. */
  const reportRun = (d: RunResult | null) => {
    if (d?.skipped) {
      toast.info(t('Skipped'), { description: d.skip_reason })
    } else if (d?.circuit_breaker_on) {
      setDialog({ kind: 'breaker' })
    } else {
      toast.success(t('Run finished'), {
        description: t('Added {{added}}, removed {{removed}}', {
          added: d?.added?.length ?? 0,
          removed: d?.removed?.length ?? 0,
        }),
      })
    }
  }

  const runOne = async (job: SyncJob, force: boolean) => {
    setRunning((s) => new Set(s).add(job.id))
    try {
      reportRun(await runJob(job.id, force))
      await refresh()
    } catch (e) {
      fail(t('Run failed'), e)
    } finally {
      setRunning((s) => {
        const next = new Set(s)
        next.delete(job.id)
        return next
      })
    }
  }

  const runEveryJob = async () => {
    setRunningAll(true)
    try {
      reportRun(await runAllJobs())
      await refresh()
    } catch (e) {
      fail(t('Run failed'), e)
    } finally {
      setRunningAll(false)
    }
  }

  const formReady = categories.isSuccess && channels.isSuccess
  const refreshing = jobs.isFetching || categories.isFetching

  const newJobButton = (
    <Button
      size='sm'
      disabled={!formReady}
      onClick={() => {
        setSaveError(null)
        setDialog({ kind: 'create' })
      }}
    >
      <Plus />
      {t('New job')}
    </Button>
  )

  let list
  if (jobs.isPending) {
    list = <LoadingState />
  } else if (jobs.isError && jobs.data === undefined) {
    // A failed read is an error, never an empty job list.
    list = (
      <ErrorState
        title={t('Could not load the sync jobs')}
        description={toApiError(jobs.error).message}
        onRetry={() => void jobs.refetch()}
      />
    )
  } else if ((jobs.data ?? []).length === 0) {
    list = (
      <EmptyState
        icon={CloudDownload}
        bordered
        title={t('No sync jobs yet')}
        description={t(
          'Create a job to import free OpenRouter models into a channel.'
        )}
        action={newJobButton}
      />
    )
  } else {
    list = (
      <div className='flex flex-col gap-2'>
        {jobs.isError && (
          <Alert variant='destructive'>
            <AlertDescription>
              {t('Refreshing the job list failed. {{message}}', {
                message: toApiError(jobs.error).message,
              })}
            </AlertDescription>
          </Alert>
        )}
        <div className='overflow-x-auto rounded-lg border'>
          <Table data-testid='sync-jobs-table'>
            <TableHeader>
              <TableRow>
                <TableHead>{t('Name')}</TableHead>
                <TableHead>{t('Target channel')}</TableHead>
                <TableHead>{t('Categories')}</TableHead>
                <TableHead>{t('Top N')}</TableHead>
                <TableHead>{t('Schedule')}</TableHead>
                <TableHead>{t('Enabled')}</TableHead>
                <TableHead>{t('Last run')}</TableHead>
                <TableHead>{t('Actions')}</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {(jobs.data ?? []).map((job) => {
                const busy = running.has(job.id)
                return (
                  <TableRow key={job.id} data-testid={`sync-job-${job.id}`}>
                    <TableCell className='font-medium'>{job.name}</TableCell>
                    <TableCell className='whitespace-nowrap'>
                      {channelLabel(job.target_channel_id, channels.data)}
                    </TableCell>
                    <TableCell>
                      <div className='flex flex-wrap gap-1'>
                        {parseCategories(job.categories).map((k) => (
                          <StatusBadge
                            key={k}
                            label={categoryLabel(
                              k,
                              categories.data?.find((c) => c.key === k)?.label,
                              t
                            )}
                            variant='blue'
                            copyable={false}
                          />
                        ))}
                      </div>
                    </TableCell>
                    <TableCell>
                      {job.top_n > 0 ? job.top_n : t('No limit')}
                    </TableCell>
                    <TableCell className='whitespace-nowrap'>
                      {scheduleLabel(job.schedule, t)}
                    </TableCell>
                    <TableCell>
                      <StatusBadge
                        label={job.enabled ? t('Yes') : t('No')}
                        variant={job.enabled ? 'success' : 'neutral'}
                        copyable={false}
                      />
                    </TableCell>
                    <TableCell>
                      <div className='flex max-w-56 flex-col gap-0.5'>
                        <span className='text-muted-foreground whitespace-nowrap'>
                          {job.last_run_at
                            ? dayjs(job.last_run_at).format(
                                'YYYY-MM-DD HH:mm:ss'
                              )
                            : '—'}
                        </span>
                        {job.last_error ? (
                          <span
                            className='text-destructive truncate text-xs'
                            title={job.last_error}
                          >
                            {job.last_error}
                          </span>
                        ) : null}
                      </div>
                    </TableCell>
                    <TableCell>
                      <div className='flex flex-wrap gap-1.5'>
                        <Button
                          variant='outline'
                          size='sm'
                          onClick={() => setDialog({ kind: 'preview', job })}
                        >
                          <Eye />
                          {t('Preview')}
                        </Button>
                        <Button
                          size='sm'
                          disabled={busy}
                          onClick={() => void runOne(job, false)}
                        >
                          <Play />
                          {t('Run now')}
                        </Button>
                        <Button
                          variant='outline'
                          size='sm'
                          disabled={busy}
                          onClick={() => setDialog({ kind: 'force', job })}
                        >
                          <Zap />
                          {t('Force run')}
                        </Button>
                        <Button
                          variant='outline'
                          size='sm'
                          disabled={!formReady}
                          onClick={() => {
                            setSaveError(null)
                            setDialog({ kind: 'edit', job })
                          }}
                        >
                          <Pencil />
                          {t('Edit')}
                        </Button>
                        <Button
                          variant='destructive'
                          size='sm'
                          onClick={() => setDialog({ kind: 'delete', job })}
                        >
                          <Trash2 />
                          {t('Delete')}
                        </Button>
                      </div>
                    </TableCell>
                  </TableRow>
                )
              })}
            </TableBody>
          </Table>
        </div>
      </div>
    )
  }

  const formDeps = (categories.isError || channels.isError) && (
    <Alert variant='destructive' data-testid='sync-deps-error'>
      <AlertDescription className='flex flex-wrap items-center gap-2'>
        <span>
          {t(
            'Could not load the category or channel list, so jobs cannot be created or edited. {{message}}',
            {
              message: toApiError(categories.error ?? channels.error).message,
            }
          )}
        </span>
        <Button
          variant='outline'
          size='sm'
          onClick={() => {
            void categories.refetch()
            void channels.refetch()
          }}
        >
          {t('Retry')}
        </Button>
      </AlertDescription>
    </Alert>
  )

  const target =
    dialog?.kind === 'delete' || dialog?.kind === 'force' ? dialog.job : null

  return (
    <>
      <SectionPageLayout>
        <SectionPageLayout.Title>
          {t('OpenRouter free-model sync')}
        </SectionPageLayout.Title>
        <SectionPageLayout.Actions>
          <Button
            variant='outline'
            size='sm'
            disabled={refreshing}
            onClick={() => void refresh()}
          >
            <RefreshCw className={refreshing ? 'animate-spin' : ''} />
            {t('Refresh')}
          </Button>
          <Button
            variant='outline'
            size='sm'
            disabled={runningAll}
            onClick={() => setDialog({ kind: 'run-all' })}
          >
            <Play />
            {t('Run all enabled')}
          </Button>
          {newJobButton}
        </SectionPageLayout.Actions>
        <SectionPageLayout.Content>
          <div className='mx-auto flex w-full max-w-6xl flex-col gap-4'>
            <p className='text-muted-foreground text-sm'>
              {t(
                'Pulls free models from OpenRouter on a schedule or on demand and imports them into a target channel by category and ranking. The sync engine is a single global engine, so several jobs can point at the same channel without stepping on each other, and models added by hand are never removed.'
              )}
            </p>
            <ApiPoolCard />
            {formDeps}
            {list}
          </div>
        </SectionPageLayout.Content>
      </SectionPageLayout>

      {(dialog?.kind === 'create' || dialog?.kind === 'edit') && (
        <JobFormSheet
          key={dialog.kind === 'edit' ? dialog.job.id : 'new'}
          job={dialog.kind === 'edit' ? dialog.job : undefined}
          channels={channels.data ?? []}
          categories={categories.data ?? []}
          saving={save.isPending}
          serverError={saveError}
          onSubmit={(body) =>
            save.mutate({
              id: dialog.kind === 'edit' ? dialog.job.id : undefined,
              body,
            })
          }
          onClose={closeDialog}
        />
      )}
      {dialog?.kind === 'preview' && (
        <PreviewDialog job={dialog.job} onClose={closeDialog} />
      )}

      <ConfirmDialog
        open={dialog?.kind === 'delete'}
        onOpenChange={(open) => {
          if (!open) closeDialog()
        }}
        title={t('Delete job "{{name}}"?', { name: target?.name ?? '' })}
        desc={t(
          'Models already imported are not removed right away. The next sync recomputes them from the remaining jobs.'
        )}
        confirmText={t('Delete')}
        destructive
        isLoading={remove.isPending}
        handleConfirm={() =>
          dialog?.kind === 'delete' && remove.mutate(dialog.job)
        }
      />
      <ConfirmDialog
        open={dialog?.kind === 'force'}
        onOpenChange={(open) => {
          if (!open) closeDialog()
        }}
        title={t('Force run "{{name}}"?', { name: target?.name ?? '' })}
        desc={t(
          'A forced run ignores the schedule and resets the circuit-breaker baseline to what OpenRouter returns now. Use it only when you are sure the upstream catalogue really changed.'
        )}
        confirmText={t('Force run')}
        handleConfirm={() => {
          if (dialog?.kind !== 'force') return
          const job = dialog.job
          closeDialog()
          void runOne(job, true)
        }}
      />
      <ConfirmDialog
        open={dialog?.kind === 'run-all'}
        onOpenChange={(open) => {
          if (!open) closeDialog()
        }}
        title={t('Run every enabled job now?')}
        desc={t(
          'Every enabled job is treated as due and runs once, regardless of its schedule.'
        )}
        confirmText={t('Run all enabled')}
        handleConfirm={() => {
          closeDialog()
          void runEveryJob()
        }}
      />
      <Dialog
        open={dialog?.kind === 'breaker'}
        onOpenChange={(open) => {
          if (!open) closeDialog()
        }}
      >
        <DialogContent>
          <DialogHeader>
            <DialogTitle>{t('Circuit breaker tripped')}</DialogTitle>
            <DialogDescription>
              {t(
                'This run fetched suspiciously few models, so nothing was written. If you are sure the upstream really changed, use Force run to reset the baseline.'
              )}
            </DialogDescription>
          </DialogHeader>
          <DialogFooter>
            <Button onClick={closeDialog}>{t('Got it')}</Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </>
  )
}
