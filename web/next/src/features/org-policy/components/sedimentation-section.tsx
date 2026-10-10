import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { ConfirmDialog } from '@/components/confirm-dialog'
import { ErrorState } from '@/components/error-state'
import { LoadingState } from '@/components/loading-state'
import { Badge } from '@/components/ui/badge'
import { Switch } from '@/components/ui/switch'
import { toApiError } from '@/lib/api'

import { policyKey, saveSedimentation, sedimentationQueryOptions } from '../api'

/**
 * Opt-in archiving of masked prompt/reply text. Default off; every flip goes
 * through a confirmation because turning it on starts storing user text and
 * turning it off is a promise that storing stops.
 */
export function SedimentationSection() {
  const { t } = useTranslation()
  const qc = useQueryClient()
  const q = useQuery(sedimentationQueryOptions)
  // The value the user asked for, held while the confirmation is open.
  const [pending, setPending] = useState<boolean | null>(null)

  const save = useMutation({
    mutationFn: (consent: boolean) => saveSedimentation(consent),
    onSuccess: async () => {
      toast.success(t('Saved'))
      setPending(null)
      await qc.invalidateQueries({ queryKey: [...policyKey, 'sedimentation'] })
    },
    onError: (e) => {
      setPending(null)
      toast.error(toApiError(e).message)
    },
  })

  if (q.isPending) return <LoadingState />
  if (q.isError) {
    return (
      <ErrorState
        title={t('Failed to load the data archiving setting')}
        description={q.error.message}
        onRetry={() => void q.refetch()}
      />
    )
  }

  const on = q.data
  return (
    <div className='space-y-4'>
      <div className='flex flex-wrap items-start justify-between gap-4 rounded-lg border p-4'>
        <div className='max-w-prose space-y-1'>
          <div className='flex items-center gap-2'>
            <h3 className='text-sm font-medium'>
              {t('Archive request and reply text')}
            </h3>
            <Badge variant={on ? 'default' : 'outline'}>
              {on ? t('On') : t('Off')}
            </Badge>
          </div>
          <p className='text-muted-foreground text-xs'>
            {t('Off by default. When on, the text of requests and replies is kept for a limited time so your administrators can review it in usage logs.')}
          </p>
        </div>
        <Switch
          checked={on}
          disabled={save.isPending}
          aria-label={t('Archive request and reply text')}
          onCheckedChange={(next) => setPending(next)}
        />
      </div>
      <ul className='text-muted-foreground list-disc space-y-1 pl-5 text-xs'>
        <li>
          {t('Only archived when the organization and key retention is "Full".')}
        </li>
        <li>{t('Content rules are applied first: only the masked text is archived.')}</li>
        <li>
          {t('Archived text is deleted automatically after the retention period (30 days by default).')}
        </li>
        <li>
          {t('Never archived for channels that keep no data, or for requests that decline data collection.')}
        </li>
        <li>{t('Turning it off stops archiving right away and deletes the text already kept.')}</li>
      </ul>
      <ConfirmDialog
        open={pending !== null}
        onOpenChange={(open) => {
          if (!open) setPending(null)
        }}
        title={
          pending
            ? t('Start archiving request and reply text?')
            : t('Stop archiving request and reply text?')
        }
        desc={
          pending
            ? t('From now on, masked request and reply text of new calls is stored and readable by administrators until it expires.')
            : t('New calls will no longer have their text archived. Text already archived stays until it expires.')
        }
        confirmText={pending ? t('Start archiving') : t('Stop archiving')}
        isLoading={save.isPending}
        handleConfirm={() => {
          if (pending !== null) save.mutate(pending)
        }}
      />
    </div>
  )
}
