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
import { useNavigate } from '@tanstack/react-router'
import type { PaginationState, Updater } from '@tanstack/react-table'
import { useCallback, useEffect, useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { SectionPageLayout } from '@/components/section-page-layout'
import { Tabs, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { useQuota } from '@/hooks/use-quota'
import { useUserAccess } from '@/hooks/use-admin'

import { fetchLogStat, fetchLogs } from './api'
import { ExportButton } from './components/export-button'
import { LogDetailSheet } from './components/log-detail-sheet'
import { StatHeader } from './components/stat-header'
import { UsageLogsTable } from './components/usage-logs-table'
import {
  EMPTY_FILTERS,
  PAGE_SIZE,
  normalizeList,
  type LogFilters,
  type UsageLog,
} from './lib/logs'
import {
  filtersFromSearch,
  searchFromFilters,
  type UsageLogsSearch,
} from './lib/url-search'

export function UsageLogsPage(props: { search: UsageLogsSearch }) {
  const { t } = useTranslation()
  const navigate = useNavigate()
  const { config } = useQuota()
  const access = useUserAccess()
  // The tenant-wide scope 403s for anyone who is not a tenant admin.
  const canSeeAll = access.isTenantAdmin

  const fromUrl = useMemo(
    () => filtersFromSearch(props.search, EMPTY_FILTERS),
    [props.search]
  )

  // `draft` is what the inputs show; `applied` is what the queries ran with.
  // URL is the shareable source (Activity drill-down / back); apply also
  // writes local state so Search works even when navigate is a no-op in tests.
  const [draft, setDraft] = useState<LogFilters>(fromUrl)
  const [applied, setApplied] = useState<LogFilters>(fromUrl)
  const [pagination, setPagination] = useState<PaginationState>({
    pageIndex: 0,
    pageSize: PAGE_SIZE,
  })
  const [selected, setSelected] = useState<UsageLog | null>(null)

  // Browser back / Activity deep-link: hydrate draft+applied from the URL.
  useEffect(() => {
    setDraft((d) => ({ ...fromUrl, tenantWide: d.tenantWide }))
    setApplied((a) => ({ ...fromUrl, tenantWide: a.tenantWide }))
    setPagination((p) => ({ ...p, pageIndex: 0 }))
  }, [fromUrl])

  const effective: LogFilters = {
    ...applied,
    tenantWide: canSeeAll && applied.tenantWide,
  }
  const page = pagination.pageIndex + 1

  const list = useQuery({
    queryKey: ['usage-logs', 'list', effective, page, pagination.pageSize],
    queryFn: () => fetchLogs(effective, page, pagination.pageSize),
    retry: false,
    placeholderData: (previous) => previous,
  })
  const stat = useQuery({
    queryKey: ['usage-logs', 'stat', effective],
    queryFn: () => fetchLogStat(effective),
    retry: false,
  })

  const patchDraft = useCallback(
    (patch: Partial<LogFilters>) => setDraft((d) => ({ ...d, ...patch })),
    []
  )
  const commitFilters = (next: LogFilters) => {
    setDraft(next)
    setApplied(next)
    setPagination((p) => ({ ...p, pageIndex: 0 }))
    void navigate({
      to: '/usage-logs',
      search: searchFromFilters(next),
      replace: true,
    })
  }
  const apply = () => commitFilters(draft)
  const reset = () => {
    commitFilters({
      ...EMPTY_FILTERS,
      tenantWide: effective.tenantWide,
    })
  }
  const changeScope = (tenantWide: boolean) => {
    // Another scope is another result set: back to page one, and a username
    // filter only exists in the tenant-wide view.
    setDraft((d) => ({ ...d, tenantWide, username: '' }))
    setApplied((a) => ({ ...a, tenantWide, username: '' }))
    setPagination((p) => ({ ...p, pageIndex: 0 }))
  }
  const changePagination = (updater: Updater<PaginationState>) =>
    setPagination((prev) => {
      const next = typeof updater === 'function' ? updater(prev) : updater
      // A new page size invalidates the page index.
      return next.pageSize === prev.pageSize
        ? next
        : { pageIndex: 0, pageSize: next.pageSize }
    })

  const { logs, total } = normalizeList(list.data)

  return (
    <>
      <SectionPageLayout>
        <SectionPageLayout.Title>{t('Usage Logs')}</SectionPageLayout.Title>
        <SectionPageLayout.Actions>
          {canSeeAll && (
            <Tabs
              value={effective.tenantWide ? 'all' : 'mine'}
              onValueChange={(v) => changeScope(v === 'all')}
            >
              <TabsList aria-label={t('View scope')}>
                <TabsTrigger value='mine'>{t('Only mine')}</TabsTrigger>
                <TabsTrigger value='all'>{t('All users')}</TabsTrigger>
              </TabsList>
            </Tabs>
          )}
        </SectionPageLayout.Actions>
        <SectionPageLayout.Content>
          <div className='flex flex-col gap-3'>
            <StatHeader
              stat={stat.data}
              loading={stat.isPending}
              failed={stat.isError}
              money={config}
            />
            <UsageLogsTable
              rows={logs}
              total={total}
              isLoading={list.isPending}
              isFetching={list.isFetching}
              error={list.isError ? list.error : null}
              onRetry={() => void list.refetch()}
              pagination={pagination}
              onPaginationChange={changePagination}
              isStaff={access.isPlatformStaff}
              tenantWide={effective.tenantWide}
              onOpen={setSelected}
              draft={{ ...draft, tenantWide: effective.tenantWide }}
              applied={effective}
              onFilterChange={patchDraft}
              onSearch={apply}
              onReset={reset}
              toolbarExtra={<ExportButton filters={effective} />}
            />
          </div>
        </SectionPageLayout.Content>
      </SectionPageLayout>

      <LogDetailSheet
        log={selected}
        tenantWide={effective.tenantWide}
        isStaff={access.isPlatformStaff}
        onClose={() => setSelected(null)}
      />
    </>
  )
}
