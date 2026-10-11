import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Download, Inbox } from 'lucide-react'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { EmptyState } from '@/components/empty-state'
import { ErrorState } from '@/components/error-state'
import { PageHeader } from '@/components/layout/page-header'
import { LoadingState } from '@/components/loading-state'
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from '@/components/ui/card'
import { Input } from '@/components/ui/input'
import { NativeSelect, NativeSelectOption } from '@/components/ui/native-select'
import {
  Table,
  TableBody,
  TableCell,
  TableFooter,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table'
import { formatQuota } from '@/lib/money'
import { useMoneyConfig } from '@/lib/status'
import { currentUserQueryOptions } from '@/lib/user'

import {
  downloadStatementCsv,
  fetchInvoices,
  fetchStatement,
  fetchWallet,
  redeemCode,
} from './api'
import { REDEEM_CODE_LENGTH, redeemErrorMessage } from './lib/redeem'
import {
  GROUP_BYS,
  currentMonth,
  findMismatches,
  formatCny,
  formatCny4,
  isValidMonth,
  rowName,
  type GroupBy,
} from './lib/statement'

const WALLET_KEY = ['org-billing', 'wallet']

function BalanceCard() {
  const { t } = useTranslation()
  const money = useMoneyConfig()
  const user = useQuery(currentUserQueryOptions)
  const wallet = useQuery({
    queryKey: WALLET_KEY,
    queryFn: fetchWallet,
    retry: false,
  })

  return (
    <Card>
      <CardHeader>
        <CardTitle>{t('Balance')}</CardTitle>
        <CardDescription>
          {t('Platform wallet and remaining quota')}
        </CardDescription>
      </CardHeader>
      <CardContent className='grid gap-4 sm:grid-cols-3'>
        <div data-testid='wallet-balance'>
          <div className='text-muted-foreground text-xs'>
            {t('Wallet balance')}
          </div>
          {wallet.isError && (
            <div role='alert' className='text-destructive text-sm'>
              {t('Wallet unavailable')}: {wallet.error.message}
            </div>
          )}
          {!wallet.isError && (
            <div className='text-xl font-semibold tabular-nums'>
              {wallet.isSuccess ? formatCny(wallet.data.balance) : '...'}
            </div>
          )}
        </div>
        <div data-testid='wallet-available'>
          <div className='text-muted-foreground text-xs'>
            {t('Available (excl. frozen)')}
          </div>
          <div className='text-xl font-semibold tabular-nums'>
            {wallet.isSuccess ? formatCny(wallet.data.available) : '--'}
          </div>
        </div>
        <div data-testid='remaining-quota'>
          <div className='text-muted-foreground text-xs'>
            {t('Remaining quota')}
          </div>
          {user.isError ? (
            <div role='alert' className='text-destructive text-sm'>
              {user.error.message}
            </div>
          ) : (
            <div className='text-xl font-semibold tabular-nums'>
              {formatQuota(user.data?.remaining_quota, money)}
            </div>
          )}
        </div>
      </CardContent>
    </Card>
  )
}

function RedeemCard() {
  const { t } = useTranslation()
  const money = useMoneyConfig()
  const qc = useQueryClient()
  const [code, setCode] = useState('')
  const [added, setAdded] = useState<number | null>(null)

  const redeem = useMutation({
    mutationFn: (key: string) => redeemCode(key),
    onSuccess: (res) => {
      setAdded(res.quota_added)
      setCode('')
      // Re-read the balances instead of doing local arithmetic.
      void qc.invalidateQueries({ queryKey: WALLET_KEY })
      void qc.invalidateQueries({ queryKey: currentUserQueryOptions.queryKey })
    },
    onError: (err) => {
      setAdded(null)
      toast.error(redeemErrorMessage(err, t))
    },
  })

  const ready = code.length === REDEEM_CODE_LENGTH && !redeem.isPending

  return (
    <Card>
      <CardHeader>
        <CardTitle>{t('Redeem a code')}</CardTitle>
        <CardDescription>
          {t('Enter a {{n}}-character redemption code to add quota.', {
            n: REDEEM_CODE_LENGTH,
          })}
        </CardDescription>
      </CardHeader>
      <CardContent>
        <form
          className='flex flex-wrap items-center gap-2'
          onSubmit={(e) => {
            e.preventDefault()
            if (ready) redeem.mutate(code)
          }}
        >
          <Input
            className='max-w-sm font-mono'
            aria-label={t('Redemption code')}
            placeholder={t('Redemption code')}
            value={code}
            maxLength={REDEEM_CODE_LENGTH}
            autoComplete='off'
            onChange={(e) => setCode(e.target.value.trim())}
          />
          <Button type='submit' disabled={!ready}>
            {redeem.isPending ? t('Redeeming...') : t('Redeem')}
          </Button>
        </form>
        {added !== null && (
          <p role='status' className='mt-3 text-sm'>
            {t('Redeemed. Quota added')}: {formatQuota(added, money)}
          </p>
        )}
      </CardContent>
    </Card>
  )
}

function StatementCard() {
  const { t } = useTranslation()
  const money = useMoneyConfig()
  const [month, setMonth] = useState(currentMonth())
  const [groupBy, setGroupBy] = useState<GroupBy>('project')
  const valid = isValidMonth(month)

  const statement = useQuery({
    queryKey: ['org-billing', 'statement', month, groupBy],
    queryFn: () => fetchStatement(month, groupBy),
    enabled: valid,
    retry: false,
  })

  const exportCsv = useMutation({
    mutationFn: () => downloadStatementCsv(month, groupBy),
    onSuccess: ({ blob, filename }) => {
      const url = URL.createObjectURL(blob)
      const a = document.createElement('a')
      a.href = url
      a.download = filename
      document.body.appendChild(a)
      a.click()
      a.remove()
      URL.revokeObjectURL(url)
    },
    onError: (err: Error) => {
      toast.error(`${t('Export failed')}: ${err.message}`)
    },
  })

  const groupLabels: Record<GroupBy, string> = {
    project: t('Project'),
    employee: t('Employee'),
    token: t('Token'),
    model: t('Model'),
  }

  let body: React.ReactNode
  if (!valid) {
    body = (
      <EmptyState icon={Inbox} title={t('Pick a month to see the statement')} />
    )
  } else if (statement.isPending) {
    body = <LoadingState />
  } else if (statement.isError) {
    body = (
      <ErrorState
        title={t('Failed to load statement')}
        description={statement.error.message}
        onRetry={() => void statement.refetch()}
      />
    )
  } else {
    const data = statement.data
    const mismatches = findMismatches(data)
    body = (
      <div className='space-y-3'>
        {mismatches.length > 0 && (
          <Alert variant='destructive' data-testid='statement-mismatch'>
            <AlertTitle>
              {t('Totals do not match the sum of the rows')}
            </AlertTitle>
            <AlertDescription>
              {mismatches.map((m) => (
                <div key={m.field}>
                  {m.field}: {t('rows sum')} {m.rowsSum} / {t('total')}{' '}
                  {m.total}
                </div>
              ))}
            </AlertDescription>
          </Alert>
        )}
        {data.rows.length === 0 ? (
          <EmptyState
            icon={Inbox}
            title={t('No billable usage in this month')}
            bordered
          />
        ) : (
          <div className='rounded-lg border'>
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>{groupLabels[groupBy]}</TableHead>
                  <TableHead className='text-right'>{t('Requests')}</TableHead>
                  <TableHead className='text-right'>{t('Quota')}</TableHead>
                  <TableHead className='text-right'>
                    {t('Charged to wallet')}
                  </TableHead>
                  <TableHead className='text-right'>
                    {t('Priced (no wallet record)')}
                  </TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {data.rows.map((r) => (
                  <TableRow key={r.key} data-testid='statement-row'>
                    <TableCell>
                      {rowName(r)}
                      {r.label && r.label !== r.key && (
                        <span className='text-muted-foreground ml-2 text-xs'>
                          {r.key}
                        </span>
                      )}
                    </TableCell>
                    <TableCell className='text-right tabular-nums'>
                      {r.requests.toLocaleString()}
                    </TableCell>
                    <TableCell className='text-right tabular-nums'>
                      {formatQuota(r.quota, money, 4)}
                    </TableCell>
                    <TableCell className='text-right tabular-nums'>
                      {formatCny4(r.charged_cny4)}
                    </TableCell>
                    <TableCell className='text-right tabular-nums'>
                      {formatCny4(r.priced_cny4)}
                    </TableCell>
                  </TableRow>
                ))}
              </TableBody>
              <TableFooter>
                <TableRow data-testid='statement-total'>
                  <TableCell className='font-semibold'>{t('Grand total')}</TableCell>
                  <TableCell className='text-right font-semibold tabular-nums'>
                    {data.totals.requests.toLocaleString()}
                  </TableCell>
                  <TableCell className='text-right font-semibold tabular-nums'>
                    {formatQuota(data.totals.quota, money, 4)}
                  </TableCell>
                  <TableCell className='text-right font-semibold tabular-nums'>
                    {formatCny4(data.totals.charged_cny4)}
                  </TableCell>
                  <TableCell className='text-right font-semibold tabular-nums'>
                    {formatCny4(data.totals.priced_cny4)}
                  </TableCell>
                </TableRow>
              </TableFooter>
            </Table>
          </div>
        )}
      </div>
    )
  }

  return (
    <Card>
      <CardHeader>
        <CardTitle>{t('Monthly statement')}</CardTitle>
        <CardDescription>
          {t('Billable usage grouped by project, employee, token or model')}
        </CardDescription>
      </CardHeader>
      <CardContent className='space-y-4'>
        <div className='flex flex-wrap items-end gap-3'>
          <label className='flex flex-col gap-1 text-xs'>
            <span className='text-muted-foreground'>{t('Month')}</span>
            <Input
              type='month'
              aria-label={t('Month')}
              value={month}
              onChange={(e) => setMonth(e.target.value)}
            />
          </label>
          <label className='flex flex-col gap-1 text-xs'>
            <span className='text-muted-foreground'>{t('Group by')}</span>
            <NativeSelect
              aria-label={t('Group by')}
              value={groupBy}
              onChange={(e) => setGroupBy(e.target.value as GroupBy)}
            >
              {GROUP_BYS.map((g) => (
                <NativeSelectOption key={g} value={g}>
                  {groupLabels[g]}
                </NativeSelectOption>
              ))}
            </NativeSelect>
          </label>
          <Button
            variant='outline'
            disabled={!valid || exportCsv.isPending}
            onClick={() => exportCsv.mutate()}
          >
            <Download className='size-4' />
            {t('Export CSV')}
          </Button>
        </div>
        {body}
      </CardContent>
    </Card>
  )
}

function InvoicesCard() {
  const { t } = useTranslation()
  const money = useMoneyConfig()
  const invoices = useQuery({
    queryKey: ['org-billing', 'invoices'],
    queryFn: fetchInvoices,
    retry: false,
  })

  let body: React.ReactNode
  if (invoices.isPending) {
    body = <LoadingState />
  } else if (invoices.isError) {
    body = (
      <ErrorState
        title={t('Failed to load invoices')}
        description={invoices.error.message}
        onRetry={() => void invoices.refetch()}
      />
    )
  } else if (invoices.data.length === 0) {
    body = <EmptyState icon={Inbox} title={t('No invoices yet')} bordered />
  } else {
    body = (
      <div className='rounded-lg border'>
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>{t('Month')}</TableHead>
              <TableHead className='text-right'>{t('Requests')}</TableHead>
              <TableHead className='text-right'>{t('Quota')}</TableHead>
              <TableHead className='text-right'>{t('Amount')}</TableHead>
              <TableHead className='text-right'>{t('Unbilled requests')}</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {invoices.data.map((i) => (
              <TableRow key={i.month} data-testid='invoice-row'>
                <TableCell>
                  {i.month}
                  {i.estimated && (
                    <Badge variant='warning' className='ml-2'>
                      {t('Estimated')}
                    </Badge>
                  )}
                </TableCell>
                <TableCell className='text-right tabular-nums'>
                  {i.request_count.toLocaleString()}
                </TableCell>
                <TableCell className='text-right tabular-nums'>
                  {formatQuota(i.quota, money, 4)}
                </TableCell>
                <TableCell className='text-right tabular-nums'>
                  {formatCny(i.amount_cny)}
                </TableCell>
                <TableCell className='text-right tabular-nums'>
                  {i.unbilled_request_count.toLocaleString()}
                </TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      </div>
    )
  }

  return (
    <Card>
      <CardHeader>
        <CardTitle>{t('Invoices')}</CardTitle>
        <CardDescription>
          {t('Your monthly spend, most recent 12 months')}
        </CardDescription>
      </CardHeader>
      <CardContent>{body}</CardContent>
    </Card>
  )
}

export function OrgBillingPage() {
  const { t } = useTranslation()
  return (
    <>
      <PageHeader title={t('Billing & Reconciliation')} />
      <BalanceCard />
      <RedeemCard />
      <StatementCard />
      <InvoicesCard />
    </>
  )
}
