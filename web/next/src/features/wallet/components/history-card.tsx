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
import { keepPreviousData, useQuery } from '@tanstack/react-query'
import { ChevronLeft, ChevronRight, Receipt } from 'lucide-react'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'

import { formatTimestampToDate } from '@/components/format'
import { EmptyState } from '@/components/empty-state'
import { ErrorState } from '@/components/error-state'
import { Button } from '@/components/ui/button'
import { Skeleton } from '@/components/ui/skeleton'
import { TitledCard } from '@/components/ui/titled-card'
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table'
import { Tabs, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { NativeSelect, NativeSelectOption } from '@/components/ui/native-select'
import { useQuota } from '@/hooks/use-quota'
import { toApiError } from '@/lib/api'
import { cn } from '@/lib/utils'

import { fetchTopups, fetchWalletTransactions, walletKeys } from '../api'
import { formatCny, formatIso, formatSignedCny } from '../lib/format'
import type { WalletInfo } from '../types'

const PAGE_SIZES = [10, 20, 50] as const
type HistoryTab = 'redemptions' | 'transactions'

function Pager(props: {
  page: number
  size: number
  total: number
  loading: boolean
  onPage: (page: number) => void
  onSize: (size: number) => void
}) {
  const { t } = useTranslation()
  const pages = Math.max(1, Math.ceil(props.total / props.size))
  const from = props.total === 0 ? 0 : (props.page - 1) * props.size + 1
  const to = Math.min(props.page * props.size, props.total)
  return (
    <div className='flex flex-col items-center gap-3 border-t pt-4 sm:flex-row sm:justify-between'>
      <div className='text-muted-foreground text-xs sm:text-sm'>
        {t('Showing {{from}}-{{to}} of {{total}}', {
          from,
          to,
          total: props.total,
        })}
      </div>
      <div className='flex items-center gap-2'>
        <NativeSelect
          aria-label={t('Rows per page')}
          value={String(props.size)}
          onChange={(e) => props.onSize(Number(e.target.value))}
        >
          {PAGE_SIZES.map((n) => (
            <NativeSelectOption key={n} value={String(n)}>
              {t('{{n}} / page', { n })}
            </NativeSelectOption>
          ))}
        </NativeSelect>
        <Button
          variant='outline'
          size='icon'
          aria-label={t('Previous page')}
          disabled={props.page <= 1 || props.loading}
          onClick={() => props.onPage(props.page - 1)}
        >
          <ChevronLeft />
        </Button>
        <span className='text-muted-foreground text-sm tabular-nums'>
          {props.page} / {pages}
        </span>
        <Button
          variant='outline'
          size='icon'
          aria-label={t('Next page')}
          disabled={props.page >= pages || props.loading}
          onClick={() => props.onPage(props.page + 1)}
        >
          <ChevronRight />
        </Button>
      </div>
    </div>
  )
}

function RowsSkeleton() {
  return (
    <div className='space-y-2' role='status'>
      {['a', 'b', 'c', 'd'].map((key) => (
        <Skeleton key={key} className='h-10 w-full' />
      ))}
    </div>
  )
}

function RedemptionsTable(props: {
  page: number
  size: number
  onPage: (page: number) => void
  onSize: (size: number) => void
}) {
  const { t } = useTranslation()
  const { formatQuota } = useQuota()
  const query = useQuery({
    queryKey: walletKeys.topups(props.page, props.size),
    queryFn: () => fetchTopups(props.page, props.size),
    placeholderData: keepPreviousData,
    retry: false,
  })

  if (query.isPending) return <RowsSkeleton />
  if (query.isError) {
    return (
      <ErrorState
        className='min-h-0 py-8'
        title={t('Unable to load top-up history')}
        description={toApiError(query.error).message}
        onRetry={() => void query.refetch()}
      />
    )
  }
  const { items, total } = query.data
  if (total === 0) {
    return (
      <EmptyState
        className='min-h-0 py-8'
        icon={Receipt}
        title={t('No top-ups yet')}
        description={t('Credits from redemption codes will appear here.')}
      />
    )
  }
  return (
    <div className='space-y-3'>
      <Table>
        <TableHeader>
          <TableRow>
            <TableHead>{t('Time')}</TableHead>
            <TableHead>{t('Details')}</TableHead>
            <TableHead className='text-end'>{t('Quota')}</TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          {items.map((row) => (
            <TableRow key={row.id}>
              <TableCell className='whitespace-nowrap'>
                {formatTimestampToDate(row.created_at)}
              </TableCell>
              <TableCell className='max-w-[28rem] truncate' title={row.content}>
                {row.content || '-'}
              </TableCell>
              <TableCell className='text-end font-mono tabular-nums'>
                {formatQuota(row.quota)}
              </TableCell>
            </TableRow>
          ))}
        </TableBody>
      </Table>
      <Pager
        page={props.page}
        size={props.size}
        total={total}
        loading={query.isFetching}
        onPage={props.onPage}
        onSize={props.onSize}
      />
    </div>
  )
}

function TransactionsTable(props: {
  page: number
  size: number
  onPage: (page: number) => void
  onSize: (size: number) => void
}) {
  const { t } = useTranslation()
  const query = useQuery({
    queryKey: walletKeys.transactions(props.page, props.size),
    queryFn: () => fetchWalletTransactions(props.page, props.size),
    placeholderData: keepPreviousData,
    retry: false,
  })

  if (query.isPending) return <RowsSkeleton />
  if (query.isError) {
    return (
      <ErrorState
        className='min-h-0 py-8'
        title={t('Unable to load wallet transactions')}
        description={toApiError(query.error).message}
        onRetry={() => void query.refetch()}
      />
    )
  }
  const { items, total } = query.data
  if (total === 0) {
    return (
      <EmptyState
        className='min-h-0 py-8'
        icon={Receipt}
        title={t('No wallet transactions yet')}
        description={t('Top-ups and charges on your wallet will appear here.')}
      />
    )
  }
  return (
    <div className='space-y-3'>
      <Table>
        <TableHeader>
          <TableRow>
            <TableHead>{t('Time')}</TableHead>
            <TableHead>{t('Type')}</TableHead>
            <TableHead>{t('Details')}</TableHead>
            <TableHead className='text-end'>{t('Amount')}</TableHead>
            <TableHead className='text-end'>{t('Balance after')}</TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          {items.map((row) => (
            <TableRow key={row.id}>
              <TableCell className='whitespace-nowrap'>
                {formatIso(row.created_at)}
              </TableCell>
              <TableCell>{row.type || '-'}</TableCell>
              <TableCell
                className='max-w-[22rem] truncate'
                title={row.description}
              >
                {row.description || row.reference_id || '-'}
              </TableCell>
              <TableCell
                className={cn(
                  'text-end font-mono tabular-nums',
                  row.amount !== null && row.amount > 0 && 'text-success',
                  row.amount !== null && row.amount < 0 && 'text-destructive'
                )}
              >
                {formatSignedCny(row.amount)}
              </TableCell>
              <TableCell className='text-end font-mono tabular-nums'>
                {formatCny(row.balance_after)}
              </TableCell>
            </TableRow>
          ))}
        </TableBody>
      </Table>
      <Pager
        page={props.page}
        size={props.size}
        total={total}
        loading={query.isFetching}
        onPage={props.onPage}
        onSize={props.onSize}
      />
    </div>
  )
}

export function HistoryCard(props: { wallet: WalletInfo | undefined }) {
  const { t } = useTranslation()
  const linked = props.wallet?.source === 'platform'
  const [tab, setTab] = useState<HistoryTab>('redemptions')
  const [page, setPage] = useState(1)
  const [size, setSize] = useState<number>(10)

  const select = (next: HistoryTab) => {
    setTab(next)
    setPage(1)
  }
  const pager = {
    page,
    size,
    onPage: setPage,
    onSize: (n: number) => {
      setSize(n)
      setPage(1)
    },
  }
  const active: HistoryTab =
    tab === 'transactions' && !linked ? 'redemptions' : tab

  return (
    <TitledCard
      title={t('Order History')}
      description={t('Your top-up and wallet records')}
      icon={<Receipt className='size-4' />}
      iconTone='info'
      disableHoverEffect
      action={
        <Tabs value={active} onValueChange={(v) => select(v as HistoryTab)}>
          <TabsList>
            <TabsTrigger value='redemptions'>{t('Redemptions')}</TabsTrigger>
            {linked && (
              <TabsTrigger value='transactions'>
                {t('Wallet transactions')}
              </TabsTrigger>
            )}
          </TabsList>
        </Tabs>
      }
    >
      {active === 'redemptions' ? (
        <RedemptionsTable {...pager} />
      ) : (
        <TransactionsTable {...pager} />
      )}
    </TitledCard>
  )
}
