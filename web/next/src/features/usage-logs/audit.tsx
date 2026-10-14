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
import { useMutation, useQuery } from '@tanstack/react-query'
import type { PaginationState, Updater } from '@tanstack/react-table'
import { Download, Loader2 } from 'lucide-react'
import { useCallback, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import {
  DataTablePage,
  DataTableRow,
  useDataTable,
} from '@/components/data-table'
import { ErrorState } from '@/components/error-state'
import { SectionPageLayout } from '@/components/section-page-layout'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { Tabs, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { useUserAccess } from '@/hooks/use-admin'
import { toApiError } from '@/lib/api'

import {
  auditActionsQueryOptions,
  auditQueryOptions,
  exportAuditCsv,
} from './audit-api'
import { AuditDetailDialog } from './components/audit-detail-dialog'
import { AuditFilterBar } from './components/audit-filter-bar'
import { ChainVerifyPanel } from './components/chain-verify-panel'
import { useAuditColumns } from './components/audit-columns'
import {
  AUDIT_PAGE_SIZE,
  EMPTY_AUDIT_FILTERS,
  auditFilterError,
  type AuditFilters,
  type AuditRow,
  type AuditScope,
} from './lib/audit'
import { downloadTextFile, fileStamp } from './lib/download'

const EMPTY_ROWS: AuditRow[] = []

function toSec(value: string): number | undefined {
  if (!value) return undefined
  const ms = new Date(value).getTime()
  return Number.isFinite(ms) ? Math.floor(ms / 1000) : undefined
}

/**
 * The audit trail. A tenant admin reads their own tenant's events; platform
 * staff additionally read the platform-wide feed and can verify its
 * tamper-evidence chain.
 */
export function AuditLogPage() {
  const { t } = useTranslation()
  const access = useUserAccess()
  const isStaff = access.isPlatformStaff

  const [scope, setScope] = useState<AuditScope>('tenant')
  const [draft, setDraft] = useState<AuditFilters>(EMPTY_AUDIT_FILTERS)
  const [applied, setApplied] = useState<AuditFilters>(EMPTY_AUDIT_FILTERS)
  const [formError, setFormError] = useState<string | null>(null)
  const [pagination, setPagination] = useState<PaginationState>({
    pageIndex: 0,
    pageSize: AUDIT_PAGE_SIZE,
  })
  const [opened, setOpened] = useState<AuditRow | null>(null)

  // Only staff have a second feed; anyone else is pinned to their tenant.
  const effectiveScope: AuditScope = isStaff ? scope : 'tenant'

  const list = useQuery({
    ...auditQueryOptions(
      effectiveScope,
      applied,
      pagination.pageIndex + 1,
      pagination.pageSize
    ),
    placeholderData: (previous) => previous,
  })
  const actionsQuery = useQuery({
    ...auditActionsQueryOptions,
    enabled: effectiveScope === 'platform',
  })

  const exporter = useMutation({
    mutationFn: () => exportAuditCsv(effectiveScope, applied),
    onSuccess: ({ csv, truncated }) => {
      downloadTextFile(`audit-events-${fileStamp()}.csv`, csv)
      if (truncated) {
        toast.warning(
          t(
            'The file holds the first part of the matching events only. Narrow the filters to export the rest.'
          )
        )
      }
    },
    onError: (e) => toast.error(toApiError(e).message),
  })

  const onOpen = useCallback((row: AuditRow) => setOpened(row), [])
  const columns = useAuditColumns(effectiveScope, onOpen)
  const rows = list.data?.items ?? EMPTY_ROWS
  const total = list.data?.total ?? 0

  const changePagination = (updater: Updater<PaginationState>) =>
    setPagination((prev) => {
      const next = typeof updater === 'function' ? updater(prev) : updater
      return next.pageSize === prev.pageSize
        ? next
        : { pageIndex: 0, pageSize: next.pageSize }
    })

  const { table } = useDataTable<AuditRow>({
    data: rows,
    columns,
    getRowId: (r) => `${r.tenantId}:${r.id}`,
    pagination,
    onPaginationChange: changePagination,
    manualPagination: true,
    manualFiltering: true,
    totalCount: total,
    enableRowSelection: false,
    enableSorting: false,
    columnVisibilityStorageKey: `usage-logs:audit:columns:${effectiveScope}`,
  })

  const patchDraft = useCallback((patch: Partial<AuditFilters>) => {
    setDraft((d) => ({ ...d, ...patch }))
    setFormError(null)
  }, [])

  const search = () => {
    const problem = auditFilterError(draft)
    if (problem === 'actor') {
      setFormError(t('The operator must be a positive whole number (user id).'))
      return
    }
    if (problem === 'range') {
      setFormError(t('The start time is after the end time.'))
      return
    }
    setFormError(null)
    setApplied(draft)
    setPagination((p) => ({ ...p, pageIndex: 0 }))
  }
  const reset = () => {
    setFormError(null)
    setDraft(EMPTY_AUDIT_FILTERS)
    setApplied(EMPTY_AUDIT_FILTERS)
    setPagination((p) => ({ ...p, pageIndex: 0 }))
  }
  const changeScope = (next: AuditScope) => {
    setScope(next)
    // Each feed has its own filters and its own result set.
    setDraft(EMPTY_AUDIT_FILTERS)
    setApplied(EMPTY_AUDIT_FILTERS)
    setFormError(null)
    setPagination((p) => ({ ...p, pageIndex: 0 }))
  }

  const columnClassName = useCallback(() => 'py-2', [])

  const toolbar = (
    <div className='space-y-2'>
      <AuditFilterBar
        table={table}
        scope={effectiveScope}
        draft={draft}
        applied={applied}
        actions={actionsQuery.data ?? []}
        onChange={patchDraft}
        onSearch={search}
        onReset={reset}
        searching={list.isFetching}
        actionStart={
          <Button
            type='button'
            variant='outline'
            disabled={exporter.isPending}
            title={t('Export uses the filters last applied with Search.')}
            onClick={() => exporter.mutate()}
          >
            {exporter.isPending ? (
              <Loader2 className='animate-spin' />
            ) : (
              <Download />
            )}
            {exporter.isPending ? t('Exporting...') : t('Export CSV')}
          </Button>
        }
      />
      {formError !== null && (
        <Alert variant='destructive'>
          <AlertDescription>{formError}</AlertDescription>
        </Alert>
      )}
    </div>
  )

  return (
    <>
      <SectionPageLayout>
        <SectionPageLayout.Title>{t('Audit log')}</SectionPageLayout.Title>
        <SectionPageLayout.Actions>
          {isStaff && (
            <Tabs
              value={effectiveScope}
              onValueChange={(v) =>
                changeScope(v === 'platform' ? 'platform' : 'tenant')
              }
            >
              <TabsList aria-label={t('View scope')}>
                <TabsTrigger value='tenant'>{t('This tenant')}</TabsTrigger>
                <TabsTrigger value='platform'>{t('Platform-wide')}</TabsTrigger>
              </TabsList>
            </Tabs>
          )}
        </SectionPageLayout.Actions>
        <SectionPageLayout.Content>
          <div className='flex flex-col gap-3'>
            {effectiveScope === 'platform' && (
              <ChainVerifyPanel
                start={toSec(applied.start)}
                end={toSec(applied.end)}
              />
            )}
            {list.isError ? (
              <div className='space-y-3'>
                {toolbar}
                <ErrorState
                  title={t('Failed to load the audit log')}
                  description={list.error.message}
                  onRetry={() => void list.refetch()}
                />
              </div>
            ) : (
              <DataTablePage
                table={table}
                columns={columns}
                fixedHeight={false}
                isLoading={list.isPending}
                isFetching={list.isFetching}
                emptyTitle={t('No audit events match')}
                skeletonKeyPrefix='audit-skeleton'
                applyHeaderSize
                getColumnClassName={columnClassName}
                toolbar={toolbar}
                renderRow={(row) => (
                  <DataTableRow
                    key={row.id}
                    row={row}
                    data-testid='audit-row'
                    className='cursor-pointer'
                    getColumnClassName={columnClassName}
                    cellRenderColumns={table.options.columns}
                    onClick={() => setOpened(row.original)}
                  />
                )}
              />
            )}
          </div>
        </SectionPageLayout.Content>
      </SectionPageLayout>

      <AuditDetailDialog
        row={opened}
        scope={effectiveScope}
        onClose={() => setOpened(null)}
      />
    </>
  )
}
