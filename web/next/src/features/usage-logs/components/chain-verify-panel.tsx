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
import { useMutation } from '@tanstack/react-query'
import { Loader2, ShieldCheck } from 'lucide-react'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { StatusBadge } from '@/components/status-badge'
import { Button } from '@/components/ui/button'
import { Card, CardContent } from '@/components/ui/card'
import { Input } from '@/components/ui/input'
import { toApiError } from '@/lib/api'

import { verifyAuditChain } from '../audit-api'
import {
  chainIntact,
  shortHash,
  type ChainBreak,
  type ChainReport,
} from '../lib/audit'
import { fmtNum } from '../lib/log-view'

interface ChainVerifyPanelProps {
  /** Unix-seconds window bounds from the applied filters. */
  start: number | undefined
  end: number | undefined
}

function Stat(props: { id: string; label: string; value: string }) {
  return (
    <div data-testid={`chain-${props.id}`} className='min-w-0'>
      <div className='text-muted-foreground text-xs'>{props.label}</div>
      <div className='font-mono text-sm font-semibold tabular-nums'>
        {props.value}
      </div>
    </div>
  )
}

function BreakLine(props: { label: string; brk: ChainBreak }) {
  const { t } = useTranslation()
  return (
    <div className='border-destructive/30 bg-destructive/5 rounded-md border p-2 text-xs'>
      <div className='font-medium'>
        {props.label} · {t('Event ID')} {props.brk.id}
      </div>
      <div className='text-muted-foreground font-mono break-all'>
        {t('expected')} {shortHash(props.brk.expected)} / {t('actual')}{' '}
        {shortHash(props.brk.actual)}
      </div>
    </div>
  )
}

/**
 * Recomputes the audit hash chain over a window of up to one server page.
 * "Verify next batch" continues from the cursor the last report returned.
 */
export function ChainVerifyPanel(props: ChainVerifyPanelProps) {
  const { t } = useTranslation()
  const [tenantId, setTenantId] = useState('')
  const [report, setReport] = useState<ChainReport | null>(null)

  const verify = useMutation({
    mutationFn: (afterId: number) =>
      verifyAuditChain({
        tenantId,
        afterId,
        start: props.start,
        end: props.end,
      }),
    onSuccess: (r) => {
      setReport(r)
      if (!chainIntact(r)) {
        toast.error(t('Audit chain verification found breaks'))
      }
    },
    onError: (e) => toast.error(toApiError(e).message),
  })

  const intact = report ? chainIntact(report) : false
  return (
    <Card size='sm' data-testid='chain-verify-panel'>
      <CardContent className='space-y-3'>
        <div className='flex flex-wrap items-end justify-between gap-3'>
          <div className='min-w-0'>
            <div className='flex items-center gap-1.5 text-sm font-medium'>
              <ShieldCheck className='size-4' aria-hidden='true' />
              {t('Tamper-evidence chain')}
            </div>
            <p className='text-muted-foreground text-xs'>
              {t(
                'Recompute the hash chain of the audit events and report any break.'
              )}
            </p>
          </div>
          <div className='flex flex-wrap items-end gap-2'>
            <Input
              aria-label={t('Tenant ID (optional)')}
              placeholder={t('Tenant ID (optional)')}
              value={tenantId}
              onChange={(e) => {
                setTenantId(e.target.value)
                setReport(null)
              }}
              className='h-8 w-44 text-sm'
            />
            <Button
              type='button'
              size='sm'
              disabled={verify.isPending}
              onClick={() => verify.mutate(0)}
            >
              {verify.isPending && <Loader2 className='animate-spin' />}
              {verify.isPending ? t('Verifying...') : t('Verify chain')}
            </Button>
            {report !== null && report.nextCursor > 0 && (
              <Button
                type='button'
                size='sm'
                variant='outline'
                disabled={verify.isPending}
                onClick={() => verify.mutate(report.nextCursor)}
              >
                {t('Verify next batch')}
              </Button>
            )}
          </div>
        </div>

        {report !== null && (
          <div data-testid='chain-result' className='space-y-2'>
            <div className='flex flex-wrap items-center gap-x-6 gap-y-2'>
              <div data-testid='chain-verdict'>
                <div className='text-muted-foreground text-xs'>
                  {t('Verdict')}
                </div>
                <StatusBadge
                  label={intact ? t('Intact') : t('Breaks found')}
                  variant={intact ? 'success' : 'danger'}
                  size='lg'
                  copyable={false}
                />
              </div>
              <Stat
                id='checked'
                label={t('Rows checked')}
                value={fmtNum(report.checked)}
              />
              <Stat
                id='hash-breaks'
                label={t('Hash breaks')}
                value={fmtNum(report.hashBreaks)}
              />
              <Stat
                id='link-breaks'
                label={t('Link breaks')}
                value={
                  report.linkChecksSkipped
                    ? t('Skipped')
                    : fmtNum(report.linkBreaks)
                }
              />
              <Stat
                id='legacy'
                label={t('Unchained rows')}
                value={fmtNum(report.legacyRows)}
              />
            </div>
            {intact && (
              <p className='text-muted-foreground text-xs'>
                {t(
                  'Every chained row in this window re-hashes to its stored value.'
                )}
              </p>
            )}
            {report.firstBreak && (
              <BreakLine
                label={t('First hash break')}
                brk={report.firstBreak}
              />
            )}
            {report.firstLinkBreak && (
              <BreakLine
                label={t('First link break')}
                brk={report.firstLinkBreak}
              />
            )}
            {report.nextCursor > 0 && (
              <p className='text-muted-foreground text-xs'>
                {t('More rows remain after this batch.')}
              </p>
            )}
          </div>
        )}
      </CardContent>
    </Card>
  )
}
