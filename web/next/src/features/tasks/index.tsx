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
import type { PaginationState, Updater } from '@tanstack/react-table'
import { useCallback, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { SectionPageLayout } from '@/components/section-page-layout'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Tabs, TabsList, TabsTrigger } from '@/components/ui/tabs'

import { mjLogsQueryOptions, taskLogsQueryOptions } from './api'
import { JobsTable } from './components/jobs-table'
import { MjFilterBar, TaskFilterBar } from './components/task-filter-bars'
import { useMjColumns, useTaskColumns } from './components/task-columns'
import {
  MjDetailsDialog,
  TaskDetailsDialog,
} from './components/task-details-dialogs'
import {
  DEFAULT_PAGE_SIZE,
  EMPTY_MJ_FILTERS,
  EMPTY_TASK_FILTERS,
  rangeError,
  type MjFilters,
  type MjLog,
  type TaskFilters,
  type TaskLog,
} from './lib/tasks'

type Section = 'jobs' | 'drawing'

const FIRST_PAGE: PaginationState = {
  pageIndex: 0,
  pageSize: DEFAULT_PAGE_SIZE,
}

function usePaging() {
  const [pagination, setPagination] = useState<PaginationState>(FIRST_PAGE)
  const change = useCallback(
    (updater: Updater<PaginationState>) =>
      setPagination((prev) => {
        const next = typeof updater === 'function' ? updater(prev) : updater
        // A new page size invalidates the page index.
        return next.pageSize === prev.pageSize
          ? next
          : { pageIndex: 0, pageSize: next.pageSize }
      }),
    []
  )
  const toFirst = useCallback(
    () => setPagination((p) => ({ ...p, pageIndex: 0 })),
    []
  )
  return { pagination, change, toFirst }
}

function JobsSection() {
  const { t } = useTranslation()
  const { pagination, change, toFirst } = usePaging()
  const [draft, setDraft] = useState<TaskFilters>(EMPTY_TASK_FILTERS)
  const [applied, setApplied] = useState<TaskFilters>(EMPTY_TASK_FILTERS)
  const [badRange, setBadRange] = useState(false)
  const [opened, setOpened] = useState<TaskLog | null>(null)

  const list = useQuery({
    ...taskLogsQueryOptions(applied, pagination.pageIndex + 1, pagination.pageSize),
    placeholderData: (previous) => previous,
  })
  const onOpen = useCallback((row: TaskLog) => setOpened(row), [])
  const columns = useTaskColumns(onOpen)

  const search = () => {
    if (rangeError(draft.start, draft.end)) {
      setBadRange(true)
      return
    }
    setBadRange(false)
    setApplied(draft)
    toFirst()
  }
  const reset = () => {
    setBadRange(false)
    setDraft(EMPTY_TASK_FILTERS)
    setApplied(EMPTY_TASK_FILTERS)
    toFirst()
  }

  return (
    <div className='space-y-3'>
      {badRange && (
        <Alert variant='destructive'>
          <AlertDescription>
            {t('The start time is after the end time.')}
          </AlertDescription>
        </Alert>
      )}
      <JobsTable<TaskLog>
        rows={list.data?.items ?? []}
        total={list.data?.total ?? 0}
        columns={columns}
        getRowId={(r) => String(r.id)}
        isLoading={list.isPending}
        isFetching={list.isFetching}
        error={list.isError ? list.error : null}
        onRetry={() => void list.refetch()}
        pagination={pagination}
        onPaginationChange={change}
        columnVisibilityStorageKey='tasks:jobs:columns'
        emptyTitle={t('No tasks yet')}
        emptyDescription={t(
          'Asynchronous jobs such as music or video generation appear here.'
        )}
        errorTitle={t('Failed to load tasks')}
        rowTestId='task-row'
        onOpen={onOpen}
        renderToolbar={(table) => (
          <TaskFilterBar
            table={table}
            draft={draft}
            applied={applied}
            onChange={(patch) => setDraft((d) => ({ ...d, ...patch }))}
            onSearch={search}
            onReset={reset}
            searching={list.isFetching}
          />
        )}
      />
      <TaskDetailsDialog log={opened} onClose={() => setOpened(null)} />
    </div>
  )
}

function DrawingSection() {
  const { t } = useTranslation()
  const { pagination, change, toFirst } = usePaging()
  const [draft, setDraft] = useState<MjFilters>(EMPTY_MJ_FILTERS)
  const [applied, setApplied] = useState<MjFilters>(EMPTY_MJ_FILTERS)
  const [badRange, setBadRange] = useState(false)
  const [opened, setOpened] = useState<MjLog | null>(null)

  const list = useQuery({
    ...mjLogsQueryOptions(applied, pagination.pageIndex + 1, pagination.pageSize),
    placeholderData: (previous) => previous,
  })
  const onOpen = useCallback((row: MjLog) => setOpened(row), [])
  const columns = useMjColumns(onOpen)

  const search = () => {
    if (rangeError(draft.start, draft.end)) {
      setBadRange(true)
      return
    }
    setBadRange(false)
    setApplied(draft)
    toFirst()
  }
  const reset = () => {
    setBadRange(false)
    setDraft(EMPTY_MJ_FILTERS)
    setApplied(EMPTY_MJ_FILTERS)
    toFirst()
  }

  return (
    <div className='space-y-3'>
      {badRange && (
        <Alert variant='destructive'>
          <AlertDescription>
            {t('The start time is after the end time.')}
          </AlertDescription>
        </Alert>
      )}
      <JobsTable<MjLog>
        rows={list.data?.items ?? []}
        total={list.data?.total ?? 0}
        columns={columns}
        getRowId={(r) => String(r.id)}
        isLoading={list.isPending}
        isFetching={list.isFetching}
        error={list.isError ? list.error : null}
        onRetry={() => void list.refetch()}
        pagination={pagination}
        onPaginationChange={change}
        columnVisibilityStorageKey='tasks:drawing:columns'
        emptyTitle={t('No drawing jobs yet')}
        emptyDescription={t('Image and video jobs appear here.')}
        errorTitle={t('Failed to load drawing jobs')}
        rowTestId='drawing-row'
        onOpen={onOpen}
        renderToolbar={(table) => (
          <MjFilterBar
            table={table}
            draft={draft}
            applied={applied}
            onChange={(patch) => setDraft((d) => ({ ...d, ...patch }))}
            onSearch={search}
            onReset={reset}
            searching={list.isFetching}
          />
        )}
      />
      <MjDetailsDialog log={opened} onClose={() => setOpened(null)} />
    </div>
  )
}

/** The caller's own asynchronous jobs: generic tasks and drawing jobs. */
export function TasksPage() {
  const { t } = useTranslation()
  const [section, setSection] = useState<Section>('jobs')
  return (
    <SectionPageLayout>
      <SectionPageLayout.Title>{t('Tasks')}</SectionPageLayout.Title>
      <SectionPageLayout.Actions>
        <Tabs
          value={section}
          onValueChange={(v) => setSection(v === 'drawing' ? 'drawing' : 'jobs')}
        >
          <TabsList aria-label={t('Job type')}>
            <TabsTrigger value='jobs'>{t('Task logs')}</TabsTrigger>
            <TabsTrigger value='drawing'>{t('Drawing logs')}</TabsTrigger>
          </TabsList>
        </Tabs>
      </SectionPageLayout.Actions>
      <SectionPageLayout.Content>
        {section === 'jobs' ? <JobsSection /> : <DrawingSection />}
      </SectionPageLayout.Content>
    </SectionPageLayout>
  )
}
