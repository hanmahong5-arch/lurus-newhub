import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { FileJson, Plus } from 'lucide-react'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { ConfirmDialog } from '@/components/confirm-dialog'
import { EmptyState } from '@/components/empty-state'
import { ErrorState } from '@/components/error-state'
import { LoadingState } from '@/components/loading-state'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table'
import { toApiError } from '@/lib/api'

import {
  applyTemplate,
  createTemplate,
  deleteTemplate,
  templatesQueryKey,
  templatesQueryOptions,
  updateTemplate,
} from '../api'
import type { ApplyRecord, ChannelTemplate, TemplateWriteBody } from '../types'
import { ApplicationsDialog } from './applications-dialog'
import { ApplyDialog } from './apply-dialog'
import { TemplateFormDialog } from './template-form-dialog'

type Dialog =
  | { kind: 'create' }
  | { kind: 'edit'; template: ChannelTemplate }
  | { kind: 'apply'; template: ChannelTemplate }
  | { kind: 'history'; template: ChannelTemplate }
  | { kind: 'delete'; template: ChannelTemplate }
  | null

function formatTime(sec: number): string {
  return sec > 0 ? new Date(sec * 1000).toLocaleString() : '--'
}

export function TemplatesPanel() {
  const { t } = useTranslation()
  const qc = useQueryClient()
  const [dialog, setDialog] = useState<Dialog>(null)
  const [records, setRecords] = useState<ApplyRecord[]>([])

  const list = useQuery(templatesQueryOptions)
  const refresh = () => qc.invalidateQueries({ queryKey: templatesQueryKey })
  const fail = (e: unknown) => {
    toast.error(toApiError(e).message)
  }

  const create = useMutation({
    mutationFn: (body: TemplateWriteBody) => createTemplate(body),
    onSuccess: async () => {
      toast.success(t('Template created'))
      setDialog(null)
      await refresh()
    },
    onError: fail,
  })

  const edit = useMutation({
    mutationFn: (v: { id: number; body: TemplateWriteBody }) =>
      updateTemplate(v.id, v.body),
    onSuccess: async () => {
      toast.success(t('Saved'))
      setDialog(null)
      await refresh()
    },
    onError: fail,
  })

  const remove = useMutation({
    mutationFn: (tpl: ChannelTemplate) => deleteTemplate(tpl.id),
    onSuccess: async () => {
      toast.success(t('Template deleted'))
      setDialog(null)
      await refresh()
    },
    onError: fail,
  })

  const apply = useMutation({
    mutationFn: (v: { template: ChannelTemplate; ids: number[] }) =>
      applyTemplate(v.template.id, v.ids),
    onSuccess: (outcome, v) => {
      setRecords((r) => [
        { ...outcome, template_name: v.template.name, at: Date.now() },
        ...r,
      ])
      setDialog(null)
      void refresh() // the server-side history changed
      const failed = outcome.results.filter((x) => !x.applied).length
      if (failed === 0) {
        toast.success(
          t('Applied to {{count}} channels', { count: outcome.results.length })
        )
      } else {
        toast.warning(
          t('{{failed}} of {{count}} channels failed', {
            failed,
            count: outcome.results.length,
          })
        )
      }
    },
    onError: fail,
  })

  const templates = list.data ?? []

  const addButton = (
    <Button onClick={() => setDialog({ kind: 'create' })}>
      <Plus />
      {t('Add template')}
    </Button>
  )

  let body
  if (list.isPending) {
    body = <LoadingState />
  } else if (list.isError && list.data === undefined) {
    body = (
      <ErrorState
        title={t('Could not load the templates')}
        description={toApiError(list.error).message}
        onRetry={() => void list.refetch()}
      />
    )
  } else if (templates.length === 0) {
    body = (
      <EmptyState
        icon={FileJson}
        title={t('No override templates yet')}
        description={t(
          'Create a template to write the same parameter and header overrides into many channels.'
        )}
        bordered
        action={addButton}
      />
    )
  } else {
    body = (
      <div className='min-w-0 rounded-lg border' data-testid='templates-table'>
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>{t('Name')}</TableHead>
              <TableHead>{t('Description')}</TableHead>
              <TableHead>{t('Version')}</TableHead>
              <TableHead>{t('Overrides')}</TableHead>
              <TableHead>{t('Updated')}</TableHead>
              <TableHead className='text-right'>{t('Actions')}</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {templates.map((tpl) => (
              <TableRow key={tpl.id}>
                <TableCell className='font-medium'>{tpl.name}</TableCell>
                <TableCell className='max-w-64 truncate'>
                  {tpl.description || '--'}
                </TableCell>
                <TableCell>v{tpl.version}</TableCell>
                <TableCell>
                  <div className='flex gap-1'>
                    {tpl.param_override !== '' && (
                      <Badge variant='outline'>{t('Parameters')}</Badge>
                    )}
                    {tpl.header_override !== '' && (
                      <Badge variant='outline'>{t('Headers')}</Badge>
                    )}
                  </div>
                </TableCell>
                <TableCell>{formatTime(tpl.updated_at)}</TableCell>
                <TableCell className='text-right'>
                  <div className='flex justify-end gap-1'>
                    <Button
                      variant='ghost'
                      size='sm'
                      onClick={() =>
                        setDialog({ kind: 'apply', template: tpl })
                      }
                    >
                      {t('Apply')}
                    </Button>
                    <Button
                      variant='ghost'
                      size='sm'
                      onClick={() =>
                        setDialog({ kind: 'history', template: tpl })
                      }
                    >
                      {t('History')}
                    </Button>
                    <Button
                      variant='ghost'
                      size='sm'
                      onClick={() => setDialog({ kind: 'edit', template: tpl })}
                    >
                      {t('Edit')}
                    </Button>
                    <Button
                      variant='ghost'
                      size='sm'
                      className='text-destructive'
                      onClick={() =>
                        setDialog({ kind: 'delete', template: tpl })
                      }
                    >
                      {t('Delete')}
                    </Button>
                  </div>
                </TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
        {list.isError && (
          <p role='alert' className='text-destructive p-3 text-sm'>
            {t('Refreshing the list failed: {{message}}', {
              message: toApiError(list.error).message,
            })}
          </p>
        )}
      </div>
    )
  }

  const confirmDelete = dialog?.kind === 'delete' ? dialog.template : null

  return (
    <div className='grid gap-4'>
      <div className='flex justify-end'>
        {templates.length > 0 && addButton}
      </div>
      {body}

      <section className='grid gap-2' data-testid='apply-records'>
        <h3 className='text-sm font-medium'>
          {t('Applications in this session')}
        </h3>
        {records.length === 0 ? (
          <p className='text-muted-foreground text-sm'>
            {t(
              'Nothing applied yet. Results of each apply are listed here until you leave the page.'
            )}
          </p>
        ) : (
          records.map((rec) => (
            <div key={rec.at} className='rounded-lg border p-3 text-sm'>
              <div className='flex flex-wrap items-center gap-2'>
                <span className='font-medium'>{rec.template_name}</span>
                <Badge variant='secondary'>v{rec.template_version}</Badge>
                <span className='text-muted-foreground'>
                  {new Date(rec.at).toLocaleString()}
                </span>
              </div>
              <ul className='mt-2 grid gap-1'>
                {rec.results.map((r) => (
                  <li key={r.channel_id} className='flex flex-wrap gap-2'>
                    <span>#{r.channel_id}</span>
                    {r.applied ? (
                      <Badge variant='default'>{t('Applied')}</Badge>
                    ) : (
                      <>
                        <Badge variant='destructive'>{t('Failed')}</Badge>
                        <span className='text-destructive'>{r.error}</span>
                      </>
                    )}
                  </li>
                ))}
              </ul>
            </div>
          ))
        )}
      </section>

      {dialog?.kind === 'create' && (
        <TemplateFormDialog
          saving={create.isPending}
          onSubmit={(b) => create.mutate(b)}
          onClose={() => setDialog(null)}
        />
      )}
      {dialog?.kind === 'edit' && (
        <TemplateFormDialog
          key={dialog.template.id}
          template={dialog.template}
          saving={edit.isPending}
          onSubmit={(b) => edit.mutate({ id: dialog.template.id, body: b })}
          onClose={() => setDialog(null)}
        />
      )}
      {dialog?.kind === 'history' && (
        <ApplicationsDialog
          key={dialog.template.id}
          template={dialog.template}
          onClose={() => setDialog(null)}
        />
      )}
      {dialog?.kind === 'apply' && (
        <ApplyDialog
          key={dialog.template.id}
          template={dialog.template}
          applying={apply.isPending}
          onApply={(ids) => apply.mutate({ template: dialog.template, ids })}
          onClose={() => setDialog(null)}
        />
      )}
      <ConfirmDialog
        open={confirmDelete !== null}
        onOpenChange={(open) => {
          if (!open) setDialog(null)
        }}
        title={t('Delete this template?')}
        desc={t(
          'Channels that already received it keep their overrides. This cannot be undone.'
        )}
        confirmText={t('Delete')}
        destructive
        isLoading={remove.isPending}
        handleConfirm={() => confirmDelete && remove.mutate(confirmDelete)}
      />
    </div>
  )
}
