import { keepPreviousData, useQuery } from '@tanstack/react-query'
import { Network, Upload } from 'lucide-react'
import { useState, type FormEvent } from 'react'
import { useTranslation } from 'react-i18next'

import { EmptyState } from '@/components/empty-state'
import { ErrorState } from '@/components/error-state'
import { PageHeader } from '@/components/layout/page-header'
import { LoadingState } from '@/components/loading-state'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { toApiError } from '@/lib/api'
import { currentUserQueryOptions, userAccess } from '@/lib/user'

import { channelListQueryOptions, PAGE_SIZE } from './api'
import { ChannelDrawer } from './components/channel-drawer'
import { ChannelsTable } from './components/channels-table'
import { ImportWizard } from './components/import-wizard'
import type { ChannelListItem } from './types'

export function OpsChannelsPage() {
  const { t } = useTranslation()
  const [page, setPage] = useState(1)
  const [keywordInput, setKeywordInput] = useState('')
  const [keyword, setKeyword] = useState('')
  const [open, setOpen] = useState<ChannelListItem | null>(null)
  const [importing, setImporting] = useState(false)

  const user = useQuery(currentUserQueryOptions)
  // UI hygiene only: every endpoint re-checks platform staff on the server.
  const canWrite = userAccess(user.data).isPlatformStaff

  const list = useQuery({
    ...channelListQueryOptions(page, keyword),
    placeholderData: keepPreviousData,
  })

  const channels = list.data?.items ?? []
  const total = list.data?.total ?? 0
  const pageCount = Math.max(1, Math.ceil(total / PAGE_SIZE))
  const nowSec = Math.floor(Date.now() / 1000)

  const search = (e: FormEvent) => {
    e.preventDefault()
    setPage(1)
    setKeyword(keywordInput.trim())
  }

  let body
  if (list.isPending) {
    body = <LoadingState />
  } else if (list.isError && list.data === undefined) {
    // A failed read is an error, never an empty list.
    body = (
      <ErrorState
        title={t('Could not load channels')}
        description={toApiError(list.error).message}
        onRetry={() => void list.refetch()}
      />
    )
  } else if (channels.length === 0 && page === 1) {
    body = (
      <EmptyState
        icon={Network}
        title={keyword !== '' ? t('No channel matches') : t('No channels yet')}
        description={
          keyword !== ''
            ? t('Try a different keyword.')
            : t('Import keys to create the first channels.')
        }
        bordered
      />
    )
  } else {
    body = (
      <div className='grid min-w-0 gap-3' data-testid='channels-body'>
        <div className='min-w-0 rounded-lg border'>
          <ChannelsTable
            channels={channels}
            nowSec={nowSec}
            onOpen={setOpen}
          />
        </div>
        <div className='flex flex-wrap items-center justify-between gap-2 text-sm'>
          <span className='text-muted-foreground'>
            {t('{{count}} channels in total', { count: total })}
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
            <span>
              {t('Page {{page}} of {{pages}}', { page, pages: pageCount })}
            </span>
            <Button
              variant='outline'
              size='sm'
              disabled={page >= pageCount}
              onClick={() => setPage((p) => p + 1)}
            >
              {t('Next')}
            </Button>
          </div>
        </div>
        {list.isError && (
          <p role='alert' className='text-destructive text-sm'>
            {t('Refreshing the list failed: {{message}}', {
              message: toApiError(list.error).message,
            })}
          </p>
        )}
      </div>
    )
  }

  return (
    <>
      <PageHeader
        title={t('Channels & Account Pools')}
        description={t(
          'Upstream accounts, their health, usage and plan settings.'
        )}
        actions={
          canWrite ? (
            <Button onClick={() => setImporting(true)}>
              <Upload />
              {t('Bulk import')}
            </Button>
          ) : undefined
        }
      />
      <form onSubmit={search} className='flex max-w-md gap-2'>
        <Input
          value={keywordInput}
          onChange={(e) => setKeywordInput(e.target.value)}
          placeholder={t('Search channels by keyword')}
          aria-label={t('Search channels')}
        />
        <Button type='submit' variant='outline'>
          {t('Search')}
        </Button>
      </form>
      {body}
      <ChannelDrawer
        channel={open}
        canWrite={canWrite}
        onClose={() => setOpen(null)}
      />
      {importing && (
        <ImportWizard channels={channels} onClose={() => setImporting(false)} />
      )}
    </>
  )
}
