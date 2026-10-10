import { useTranslation } from 'react-i18next'

import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import {
  Sheet,
  SheetContent,
  SheetDescription,
  SheetHeader,
  SheetTitle,
} from '@/components/ui/sheet'
import { useCopyToClipboard } from '@/hooks/use-copy-to-clipboard'
import { formatQuota, type MoneyConfig } from '@/lib/money'

import {
  detailFields,
  formatLogTime,
  isErrorLog,
  isSettlementFailed,
  latencyMs,
  type UsageLog,
} from '../lib/logs'
import { LogBodySection } from './log-body-section'

interface LogDetailSheetProps {
  log: UsageLog | null
  money: MoneyConfig | null
  tenantWide: boolean
  onClose: () => void
}

function Row(props: { id: string; label: string; children: React.ReactNode }) {
  return (
    <div
      data-testid={`log-detail-${props.id}`}
      className='flex items-start justify-between gap-4 border-b py-2 text-sm last:border-b-0'
    >
      <span className='text-muted-foreground shrink-0'>{props.label}</span>
      <span className='min-w-0 text-right break-all'>{props.children}</span>
    </div>
  )
}

export function LogDetailSheet(props: LogDetailSheetProps) {
  const { t } = useTranslation()
  const { copyToClipboard } = useCopyToClipboard()
  const log = props.log
  const fields = log ? detailFields(log) : null
  const requestId = fields?.requestId ?? null
  const sessionId = fields?.sessionId ?? null
  const latency = log ? latencyMs(log) : null

  return (
    <Sheet
      open={log !== null}
      onOpenChange={(open) => {
        if (!open) props.onClose()
      }}
    >
      <SheetContent className='w-full overflow-y-auto sm:max-w-md'>
        {log && fields && (
          <>
            <SheetHeader>
              <SheetTitle>{log.model_name || '--'}</SheetTitle>
              <SheetDescription>
                {formatLogTime(log.created_at)}
              </SheetDescription>
              <div className='flex flex-wrap gap-2 pt-1'>
                <Badge variant={isErrorLog(log) ? 'destructive' : 'secondary'}>
                  {isErrorLog(log) ? t('Error') : t('OK')}
                </Badge>
                {isSettlementFailed(log) && (
                  <Badge variant='warning'>{t('Settlement failed')}</Badge>
                )}
                {log.is_stream && (
                  <Badge variant='outline'>{t('Stream')}</Badge>
                )}
              </div>
            </SheetHeader>
            <div className='px-4 pb-4'>
              <Row id='model' label={t('Model')}>
                {log.model_name || '--'}
              </Row>
              <Row id='token' label={t('Token')}>
                {log.token_name || '--'}
              </Row>
              {props.tenantWide && log.username && (
                <Row id='user' label={t('User')}>
                  {log.username}
                </Row>
              )}
              <Row id='prompt' label={t('Prompt tokens')}>
                {log.prompt_tokens}
              </Row>
              <Row id='completion' label={t('Completion tokens')}>
                {log.completion_tokens}
              </Row>
              <Row id='cost' label={t('Cost')}>
                {formatQuota(log.quota, props.money, 4)}
              </Row>
              <Row id='duration' label={t('Duration')}>
                {latency !== null ? `${latency}ms` : '--'}
              </Row>
              {fields.firstResponseMs !== null && (
                <Row id='frt' label={t('First response')}>
                  {fields.firstResponseMs}ms
                </Row>
              )}
              {fields.cacheReadTokens !== null && (
                <Row id='cache-read' label={t('Cache read tokens')}>
                  {fields.cacheReadTokens}
                </Row>
              )}
              {fields.cacheWriteTokens !== null && (
                <Row id='cache-write' label={t('Cache write tokens')}>
                  {fields.cacheWriteTokens}
                </Row>
              )}
              {fields.endpoint !== null && (
                <Row id='endpoint' label={t('Endpoint')}>
                  {fields.endpoint}
                </Row>
              )}
              {requestId !== null && (
                <Row id='request-id' label={t('Request ID')}>
                  <span className='inline-flex items-center gap-2'>
                    {requestId}
                    <Button
                      variant='ghost'
                      size='xs'
                      onClick={() => void copyToClipboard(requestId)}
                    >
                      {t('Copy')}
                    </Button>
                  </span>
                </Row>
              )}
              {sessionId !== null && (
                <Row id='session-id' label={t('Session ID')}>
                  <span className='inline-flex items-center gap-2'>
                    {sessionId}
                    <Button
                      variant='ghost'
                      size='xs'
                      onClick={() => void copyToClipboard(sessionId)}
                    >
                      {t('Copy')}
                    </Button>
                  </span>
                </Row>
              )}
              {log.content && (
                <Row id='content' label={t('Content')}>
                  {log.content}
                </Row>
              )}
              {requestId !== null && <LogBodySection key={requestId} requestId={requestId} />}
            </div>
          </>
        )}
      </SheetContent>
    </Sheet>
  )
}
