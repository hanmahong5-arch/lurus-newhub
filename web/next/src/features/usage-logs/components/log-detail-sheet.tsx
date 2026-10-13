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
import { useTranslation } from 'react-i18next'

import { formatTimestampToDate } from '@/components/format'
import { CopyButton } from '@/components/copy-button'
import { StatusBadge } from '@/components/status-badge'
import { Badge } from '@/components/ui/badge'
import {
  Sheet,
  SheetContent,
  SheetDescription,
  SheetHeader,
  SheetTitle,
} from '@/components/ui/sheet'
import { useQuota } from '@/hooks/use-quota'
import { usdToQuota } from '@/lib/money'

import {
  attemptVariant,
  chargedCny,
  durationSec,
  errorFacts,
  fmtNum,
  formatCny,
  formatRatio,
  formatSeconds,
  groupRatio,
  isDisplayableLogType,
  mappedModel,
  perCallPriceUsd,
  ratioFacts,
  routeAttempts,
  tokenBreakdown,
  tokensPerSecond,
} from '../lib/log-view'
import {
  detailFields,
  isErrorLog,
  isSettlementFailed,
  parseOther,
  type UsageLog,
} from '../lib/logs'
import { DetailRow, DetailSection } from './detail-layout'
import { TypeBadge } from './log-cells'
import { LogBodySection } from './log-body-section'

interface LogDetailSheetProps {
  log: UsageLog | null
  tenantWide: boolean
  isStaff: boolean
  onClose: () => void
}

function Copyable(props: { value: string }) {
  return (
    <span className='inline-flex items-center gap-1'>
      <span className='min-w-0'>{props.value}</span>
      <CopyButton
        value={props.value}
        size='icon'
        className='size-5'
        iconClassName='size-3'
      />
    </span>
  )
}

export function LogDetailSheet(props: LogDetailSheetProps) {
  const log = props.log

  return (
    <Sheet
      open={log !== null}
      onOpenChange={(open) => {
        if (!open) props.onClose()
      }}
    >
      <SheetContent className='w-full overflow-y-auto sm:max-w-lg'>
        {log && (
          <LogDetailBody
            log={log}
            tenantWide={props.tenantWide}
            isStaff={props.isStaff}
          />
        )}
      </SheetContent>
    </Sheet>
  )
}

interface BodyProps {
  log: UsageLog
  tenantWide: boolean
  isStaff: boolean
}

function LogDetailBody(props: BodyProps) {
  const { t } = useTranslation()
  const { formatLogQuota, config } = useQuota()
  const { log } = props
  // Per-call prices are recorded in USD; show them in the display currency.
  const formatUsd = (usd: number) =>
    config === null ? '--' : formatLogQuota(usdToQuota(usd, config))
  const fields = detailFields(log)
  const other = parseOther(log)
  const requestId = fields.requestId
  const sessionId = fields.sessionId
  const model = mappedModel(log)
  const tokens = tokenBreakdown(log)
  const duration = durationSec(log)
  const tps = tokensPerSecond(log)
  const charged = chargedCny(log)
  const perCall = perCallPriceUsd(log)
  const ratios = ratioFacts(log)
  const gRatio = groupRatio(log)
  const attempts = props.isStaff ? routeAttempts(log) : []
  const err = errorFacts(log)
  const displayable = isDisplayableLogType(log.type)
  const upstreamRequestId =
    typeof other?.upstream_request_id === 'string' && other.upstream_request_id
      ? other.upstream_request_id
      : null
  const reasoning =
    typeof other?.reasoning_effort === 'string' ? other.reasoning_effort : ''

  return (
    <>
      <SheetHeader>
        <SheetTitle className='break-all'>{log.model_name || '--'}</SheetTitle>
        <SheetDescription>
          {formatTimestampToDate(log.created_at)}
        </SheetDescription>
        <div className='flex flex-wrap gap-2 pt-1'>
          <TypeBadge type={log.type} />
          <Badge variant={isErrorLog(log) ? 'destructive' : 'secondary'}>
            {isErrorLog(log) ? t('Error') : t('OK')}
          </Badge>
          {isSettlementFailed(log) && (
            <Badge variant='warning'>{t('Settlement failed')}</Badge>
          )}
          {log.is_stream && <Badge variant='outline'>{t('Stream')}</Badge>}
        </div>
      </SheetHeader>

      <div className='space-y-3 px-4 pb-4'>
        <DetailSection label={t('Request')}>
          <DetailRow
            testId='log-detail-model'
            label={t('Model')}
            value={log.model_name || '--'}
            mono
          />
          {model.upstream && (
            <DetailRow
              testId='log-detail-upstream-model'
              label={t('Routed to')}
              value={model.upstream}
              mono
            />
          )}
          <DetailRow
            testId='log-detail-token'
            label={t('Token')}
            value={log.token_name || '--'}
          />
          {log.group && (
            <DetailRow
              label={t('Group')}
              value={
                gRatio === null
                  ? log.group
                  : `${log.group} · ${formatRatio(gRatio)}x`
              }
            />
          )}
          {props.tenantWide && log.username && (
            <DetailRow
              testId='log-detail-user'
              label={t('User')}
              value={log.username}
            />
          )}
          {fields.endpoint !== null && (
            <DetailRow
              testId='log-detail-endpoint'
              label={t('Endpoint')}
              value={fields.endpoint}
              mono
            />
          )}
          {reasoning && (
            <DetailRow label={t('Reasoning effort')} value={reasoning} />
          )}
          {requestId !== null && (
            <DetailRow
              testId='log-detail-request-id'
              label={t('Request ID')}
              value={<Copyable value={requestId} />}
              mono
            />
          )}
          {sessionId !== null && (
            <DetailRow
              testId='log-detail-session-id'
              label={t('Session ID')}
              value={<Copyable value={sessionId} />}
              mono
            />
          )}
          {props.isStaff && upstreamRequestId !== null && (
            <DetailRow
              label={t('Upstream request ID')}
              value={<Copyable value={upstreamRequestId} />}
              mono
            />
          )}
          {props.isStaff && log.channel > 0 && (
            <DetailRow
              testId='log-detail-channel'
              label={t('Channel')}
              value={
                log.channel_name
                  ? `${log.channel_name} #${log.channel}`
                  : `#${log.channel}`
              }
              mono
            />
          )}
        </DetailSection>

        {displayable && (
          <DetailSection label={t('Usage')}>
            <DetailRow
              testId='log-detail-prompt'
              label={t('Prompt tokens')}
              value={fmtNum(tokens.prompt)}
              mono
            />
            <DetailRow
              testId='log-detail-completion'
              label={t('Completion tokens')}
              value={fmtNum(tokens.completion)}
              mono
            />
            {tokens.cacheRead > 0 && (
              <DetailRow
                testId='log-detail-cache-read'
                label={t('Cache read tokens')}
                value={fmtNum(tokens.cacheRead)}
                mono
              />
            )}
            {tokens.cacheWrite > 0 && (
              <DetailRow
                testId='log-detail-cache-write'
                label={t('Cache write tokens')}
                value={fmtNum(tokens.cacheWrite)}
                mono
              />
            )}
            <DetailRow
              testId='log-detail-duration'
              label={t('Duration')}
              value={duration === null ? '--' : formatSeconds(duration)}
              mono
            />
            {fields.firstResponseMs !== null && (
              <DetailRow
                testId='log-detail-frt'
                label={t('First response')}
                value={`${fields.firstResponseMs}ms`}
                mono
              />
            )}
            {tps !== null && log.is_stream && (
              <DetailRow
                label={t('Speed')}
                value={`${Math.round(tps)} t/s`}
                mono
              />
            )}
          </DetailSection>
        )}

        {displayable && (
          <DetailSection label={t('Billing')}>
            <DetailRow
              testId='log-detail-cost'
              label={t('Cost')}
              value={formatLogQuota(log.quota)}
              mono
            />
            {charged !== null && (
              <DetailRow
                testId='log-detail-charged'
                label={t('Charged to wallet')}
                value={formatCny(charged)}
                mono
              />
            )}
            {perCall !== null && (
              <DetailRow
                label={t('Per-call price')}
                value={formatUsd(perCall)}
                mono
              />
            )}
            {perCall === null && ratios.model !== null && (
              <DetailRow
                label={t('Model ratio')}
                value={`${formatRatio(ratios.model)}x`}
                mono
              />
            )}
            {perCall === null && ratios.completion !== null && (
              <DetailRow
                label={t('Completion ratio')}
                value={`${formatRatio(ratios.completion)}x`}
                mono
              />
            )}
            {ratios.cache !== null && ratios.cache !== 1 && (
              <DetailRow
                label={t('Cache ratio')}
                value={`${formatRatio(ratios.cache)}x`}
                mono
              />
            )}
            {gRatio !== null && (
              <DetailRow
                label={t('Group ratio')}
                value={`${formatRatio(gRatio)}x`}
                mono
              />
            )}
            {isSettlementFailed(log) && (
              <p
                role='status'
                className='text-warning text-xs'
                data-testid='log-detail-settlement-failed'
              >
                {t(
                  'The charge for this call may not have been applied to the wallet.'
                )}
              </p>
            )}
          </DetailSection>
        )}

        {isErrorLog(log) && (
          <DetailSection label={t('Error')} variant='danger'>
            {err.statusCode !== null && (
              <DetailRow
                label={t('HTTP status')}
                value={String(err.statusCode)}
                mono
              />
            )}
            {err.errorCode && (
              <DetailRow label={t('Error code')} value={err.errorCode} mono />
            )}
            {err.errorType && (
              <DetailRow label={t('Error type')} value={err.errorType} mono />
            )}
            {log.content && (
              <DetailRow label={t('Message')} value={log.content} />
            )}
          </DetailSection>
        )}

        {attempts.length > 0 && (
          <DetailSection label={t('Routing')} testId='log-route-attempts'>
            {attempts.map((a, i) => (
              <div
                key={`${a.channel_id}-${a.outcome}-${a.duration_ms ?? 0}-${a.status_code ?? 0}`}
                data-testid={`route-attempt-${i}`}
                className='flex flex-wrap items-center gap-2 font-mono text-xs'
              >
                <span className='text-muted-foreground'>{i + 1}.</span>
                <span className='font-medium'>
                  #{a.channel_id}
                  {a.channel_name ? ` ${a.channel_name}` : ''}
                </span>
                {a.provider && (
                  <span className='text-muted-foreground'>{a.provider}</span>
                )}
                <StatusBadge
                  label={a.outcome || t('Unknown')}
                  variant={attemptVariant(a.outcome)}
                  size='sm'
                  copyable={false}
                />
                {a.status_code !== null && (
                  <span className='text-muted-foreground'>{a.status_code}</span>
                )}
                {a.error_code && (
                  <span className='text-muted-foreground/70'>
                    {a.error_code}
                  </span>
                )}
                {a.duration_ms !== null && (
                  <span className='text-muted-foreground'>
                    {a.duration_ms}ms
                  </span>
                )}
              </div>
            ))}
          </DetailSection>
        )}

        {!isErrorLog(log) && log.content && (
          <DetailSection label={t('Content')}>
            <p
              data-testid='log-detail-content'
              className='text-xs break-all whitespace-pre-wrap'
            >
              {log.content}
            </p>
          </DetailSection>
        )}

        {requestId !== null && (
          <LogBodySection key={requestId} requestId={requestId} />
        )}
      </div>
    </>
  )
}
