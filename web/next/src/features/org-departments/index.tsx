import {
  useMutation,
  useQuery,
  useQueryClient,
} from '@tanstack/react-query'
import { Building2, Pencil, Plus, Trash2, Users } from 'lucide-react'
import { useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { ConfirmDialog } from '@/components/confirm-dialog'
import { EmptyState } from '@/components/empty-state'
import { ErrorState } from '@/components/error-state'
import { PageHeader } from '@/components/layout/page-header'
import { LoadingState } from '@/components/loading-state'
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
import { MONEY_UNAVAILABLE, formatQuota } from '@/lib/money'
import { useMoneyConfig } from '@/lib/status'
import { currentUserQueryOptions, userAccess } from '@/lib/user'

import {
  createDepartment,
  deleteDepartment,
  departmentsQueryKey,
  departmentsQueryOptions,
  spendQueryOptions,
  updateDepartment,
} from './api'
import { DepartmentFormDialog } from './components/department-form-dialog'
import { MembersDialog } from './components/members-dialog'
import { budgetRatio, monthStartSec } from './lib/form'
import { visibleDepartments } from './lib/map'
import type { Department, DepartmentWriteBody } from './types'

type Dialog =
  | { kind: 'create' }
  | { kind: 'edit'; department: Department }
  | { kind: 'members'; department: Department }
  | { kind: 'delete'; department: Department }
  | null

export function OrgDepartmentsPage() {
  const { t } = useTranslation()
  const qc = useQueryClient()
  const money = useMoneyConfig()
  const [dialog, setDialog] = useState<Dialog>(null)
  // Fixed per mount so the query key is stable while the page is open.
  const monthStart = useMemo(() => monthStartSec(), [])

  const me = useQuery(currentUserQueryOptions)
  const access = userAccess(me.data)
  const canManage = access.isTenantAdmin
  const list = useQuery({ ...departmentsQueryOptions, enabled: me.isSuccess })
  const spend = useQuery({
    ...spendQueryOptions(monthStart),
    enabled: me.isSuccess,
  })

  const refresh = () => qc.invalidateQueries({ queryKey: departmentsQueryKey })
  const fail = (e: unknown) => {
    toast.error(toApiError(e).message)
  }

  const create = useMutation({
    mutationFn: (body: DepartmentWriteBody) => createDepartment(body),
    onSuccess: async () => {
      toast.success(t('Department created'))
      setDialog(null)
      await refresh()
    },
    onError: fail,
  })

  const edit = useMutation({
    mutationFn: (v: { id: number; body: DepartmentWriteBody }) =>
      updateDepartment(v.id, v.body),
    onSuccess: async () => {
      toast.success(t('Saved'))
      setDialog(null)
      await refresh()
    },
    onError: fail,
  })

  const remove = useMutation({
    mutationFn: (id: number) => deleteDepartment(id),
    onSuccess: async () => {
      toast.success(t('Department deleted'))
      setDialog(null)
      await refresh()
    },
    onError: fail,
  })

  const header = (
    <PageHeader
      title={t('Departments')}
      description={
        canManage
          ? t('Group keys and spend by department, set budgets and leads.')
          : t('The departments you lead. Read only.')
      }
      actions={
        canManage ? (
          <Button onClick={() => setDialog({ kind: 'create' })}>
            <Plus />
            {t('New department')}
          </Button>
        ) : undefined
      }
    />
  )

  // A lead's view is derived from the spend report, so its failure is fatal
  // for them; for an admin it only blanks the "used" column.
  const leadNeedsSpend = !canManage

  let body
  if (me.isPending) {
    body = <LoadingState />
  } else if (me.isError) {
    body = (
      <ErrorState
        title={t('Could not load your account')}
        description={toApiError(me.error).message}
        onRetry={() => void me.refetch()}
      />
    )
  } else if (list.isPending || (leadNeedsSpend && spend.isPending)) {
    body = <LoadingState />
  } else if (list.isError) {
    body = (
      <ErrorState
        title={t('Could not load the departments')}
        description={toApiError(list.error).message}
        onRetry={() => void list.refetch()}
      />
    )
  } else if (leadNeedsSpend && spend.isError) {
    body = (
      <ErrorState
        title={t('Could not load your departments')}
        description={toApiError(spend.error).message}
        onRetry={() => void spend.refetch()}
      />
    )
  } else {
    const rows = visibleDepartments(list.data, spend.data, canManage)
    if (rows.length === 0) {
      body = canManage ? (
        <EmptyState
          icon={Building2}
          title={t('No departments yet')}
          description={t(
            'Create a department to attribute keys and spend to it.'
          )}
          bordered
          action={
            <Button onClick={() => setDialog({ kind: 'create' })}>
              <Plus />
              {t('New department')}
            </Button>
          }
        />
      ) : (
        <EmptyState
          icon={Building2}
          title={t('No department to show')}
          description={t(
            'Departments you lead appear here once they have usage this month.'
          )}
          bordered
        />
      )
    } else {
      body = (
        <div className='grid min-w-0 gap-3' data-testid='departments-body'>
          {canManage && spend.isError && (
            <p role='alert' className='text-destructive text-sm'>
              {t('Could not load this month\'s usage: {{message}}', {
                message: toApiError(spend.error).message,
              })}
            </p>
          )}
          <div className='min-w-0 rounded-lg border'>
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>{t('Name')}</TableHead>
                  <TableHead>{t('External code')}</TableHead>
                  <TableHead>{t('Monthly budget')}</TableHead>
                  <TableHead>{t('Used this month')}</TableHead>
                  {canManage && (
                    <TableHead className='text-end'>{t('Actions')}</TableHead>
                  )}
                </TableRow>
              </TableHeader>
              <TableBody>
                {rows.map((d) => {
                  const used = spend.isSuccess
                    ? (spend.data.byProject[d.id] ?? 0)
                    : null
                  const ratio =
                    used === null ? null : budgetRatio(used, d.monthlyBudgetQuota)
                  return (
                    <TableRow key={d.id} data-testid={`department-row-${d.id}`}>
                      <TableCell>
                        <div className='max-w-56 truncate font-medium'>
                          {d.name}
                        </div>
                        {d.description !== '' && (
                          <div className='text-muted-foreground max-w-56 truncate text-xs'>
                            {d.description}
                          </div>
                        )}
                      </TableCell>
                      <TableCell>
                        {d.externalCode !== '' ? (
                          <code className='font-mono text-xs'>
                            {d.externalCode}
                          </code>
                        ) : (
                          <span className='text-muted-foreground'>-</span>
                        )}
                      </TableCell>
                      <TableCell>
                        {d.monthlyBudgetQuota > 0
                          ? formatQuota(d.monthlyBudgetQuota, money)
                          : t('No budget')}
                      </TableCell>
                      <TableCell>
                        <div className='grid min-w-28 gap-1'>
                          <span className='text-sm'>
                            {used === null
                              ? MONEY_UNAVAILABLE
                              : formatQuota(used, money)}
                          </span>
                          {ratio != null && (
                            <div
                              className='bg-muted h-1 w-full overflow-hidden rounded-full'
                              role='progressbar'
                              aria-valuemin={0}
                              aria-valuemax={100}
                              aria-valuenow={Math.round(ratio * 100)}
                            >
                              <div
                                className={
                                  ratio >= 0.9
                                    ? 'bg-destructive h-1'
                                    : 'bg-primary h-1'
                                }
                                style={{ width: `${ratio * 100}%` }}
                              />
                            </div>
                          )}
                        </div>
                      </TableCell>
                      {canManage && (
                        <TableCell>
                          <div className='flex items-center justify-end gap-1'>
                            <Button
                              variant='ghost'
                              size='icon-sm'
                              aria-label={t('Members of {{name}}', {
                                name: d.name,
                              })}
                              onClick={() =>
                                setDialog({ kind: 'members', department: d })
                              }
                            >
                              <Users />
                            </Button>
                            <Button
                              variant='ghost'
                              size='icon-sm'
                              aria-label={t('Edit {{name}}', { name: d.name })}
                              onClick={() =>
                                setDialog({ kind: 'edit', department: d })
                              }
                            >
                              <Pencil />
                            </Button>
                            <Button
                              variant='ghost'
                              size='icon-sm'
                              aria-label={t('Delete {{name}}', {
                                name: d.name,
                              })}
                              onClick={() =>
                                setDialog({ kind: 'delete', department: d })
                              }
                            >
                              <Trash2 />
                            </Button>
                          </div>
                        </TableCell>
                      )}
                    </TableRow>
                  )
                })}
              </TableBody>
            </Table>
          </div>
        </div>
      )
    }
  }

  const confirmDelete = dialog?.kind === 'delete' ? dialog.department : null

  return (
    <>
      {header}
      {body}

      {canManage && dialog?.kind === 'create' && (
        <DepartmentFormDialog
          money={money}
          saving={create.isPending}
          onSubmit={(b) => create.mutate(b)}
          onClose={() => setDialog(null)}
        />
      )}
      {canManage && dialog?.kind === 'edit' && (
        <DepartmentFormDialog
          key={dialog.department.id}
          department={dialog.department}
          money={money}
          saving={edit.isPending}
          onSubmit={(b) => edit.mutate({ id: dialog.department.id, body: b })}
          onClose={() => setDialog(null)}
        />
      )}
      {canManage && dialog?.kind === 'members' && (
        <MembersDialog
          key={dialog.department.id}
          department={dialog.department}
          onClose={() => setDialog(null)}
        />
      )}
      <ConfirmDialog
        open={canManage && confirmDelete !== null}
        onOpenChange={(open) => {
          if (!open) setDialog(null)
        }}
        title={t('Delete this department?')}
        desc={t(
          'Keys attached to it become unassigned and its leads lose the role if this was their last department.'
        )}
        confirmText={t('Delete')}
        destructive
        isLoading={remove.isPending}
        handleConfirm={() => confirmDelete && remove.mutate(confirmDelete.id)}
      />
    </>
  )
}
