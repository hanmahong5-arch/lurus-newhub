import { useQuery } from '@tanstack/react-query'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'

import { Badge } from '@/components/ui/badge'
import {
  Collapsible,
  CollapsibleContent,
  CollapsibleTrigger,
} from '@/components/ui/collapsible'
import { isApiError } from '@/lib/api-error'

import { fetchLogBody } from '../api'

function TextBlock(props: { id: string; label: string; text: string }) {
  return (
    <div className='space-y-1'>
      <div className='text-muted-foreground text-xs'>{props.label}</div>
      <pre
        data-testid={`log-body-${props.id}`}
        className='bg-muted max-h-64 overflow-auto rounded-md p-2 text-xs break-words whitespace-pre-wrap'
      >
        {props.text}
      </pre>
    </div>
  )
}

/**
 * Archived request/reply text of one call. Fetched only once the section is
 * opened: reading a body is audited server-side, so merely opening the row
 * must not count as a read.
 */
export function LogBodySection(props: { requestId: string }) {
  const { t } = useTranslation()
  const [open, setOpen] = useState(false)
  const q = useQuery({
    queryKey: ['usage-logs', 'body', props.requestId],
    queryFn: () => fetchLogBody(props.requestId),
    enabled: open,
    retry: false,
    staleTime: 60_000,
  })
  const notArchived =
    q.isError && isApiError(q.error) && q.error.status === 404

  return (
    <Collapsible
      open={open}
      onOpenChange={setOpen}
      className='mt-4 rounded-lg border p-3'
    >
      <CollapsibleTrigger className='w-full text-left text-sm font-medium'>
        {t('Request and reply text')}
      </CollapsibleTrigger>
      <CollapsibleContent className='space-y-3 pt-3'>
        {q.isPending && open && (
          <p className='text-muted-foreground text-xs'>{t('Loading...')}</p>
        )}
        {notArchived && (
          <p data-testid='log-body-missing' className='text-muted-foreground text-xs'>
            {t('Not archived for this call.')}
          </p>
        )}
        {q.isError && !notArchived && (
          <p role='alert' className='text-destructive text-xs'>
            {q.error.message}
          </p>
        )}
        {q.data && (
          <>
            <div className='flex flex-wrap gap-2'>
              {q.data.truncated && (
                <Badge variant='warning'>{t('Shortened for storage')}</Badge>
              )}
              {!q.data.responseCaptured && (
                <Badge variant='outline'>{t('Reply text not recorded')}</Badge>
              )}
            </div>
            <TextBlock
              id='request'
              label={t('Request')}
              text={q.data.requestBody}
            />
            {q.data.responseCaptured && (
              <TextBlock
                id='response'
                label={t('Reply')}
                text={q.data.responseText}
              />
            )}
          </>
        )}
      </CollapsibleContent>
    </Collapsible>
  )
}
