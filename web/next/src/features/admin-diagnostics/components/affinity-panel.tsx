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
import { useMutation, useQueryClient } from '@tanstack/react-query'
import { Eraser, Trash2 } from 'lucide-react'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { ErrorState } from '@/components/error-state'
import { StatusBadge } from '@/components/status-badge'
import { Button } from '@/components/ui/button'
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from '@/components/ui/card'
import { Input } from '@/components/ui/input'
import { ApiError } from '@/lib/api-error'
import { getServerErrorMessage } from '@/lib/server-error-message'

import { diagnosticsKeys, purgeAffinityKey, purgeAllAffinity } from '../api'
import type { AffinitySnapshot } from '../types'
import { PurgeAllDialog } from './purge-all-dialog'
import { Stat } from './stat'

type KeyMessage = { tone: 'ok' | 'error'; text: string }

export function AffinityPanel(props: {
  /** undefined while the read is unavailable. */
  snapshot: AffinitySnapshot | undefined
  failed: boolean
  onRetry: () => void
}) {
  const { t } = useTranslation()
  const qc = useQueryClient()
  const [key, setKey] = useState('')
  const [message, setMessage] = useState<KeyMessage | null>(null)
  const [purgeAllOpen, setPurgeAllOpen] = useState(false)

  const refetchAll = () =>
    qc.invalidateQueries({ queryKey: diagnosticsKeys.all })

  const purgeOne = useMutation({
    mutationFn: purgeAffinityKey,
    // Reported inline beside the field, not as a second global toast.
    meta: { errorToast: false },
    onSuccess: async () => {
      setMessage({ tone: 'ok', text: t('Binding purged') })
      setKey('')
      await refetchAll()
    },
    onError: (error) => {
      // A 404 is the server positively confirming there is no such binding. A
      // genuine failure (a 500 from a store outage) must never be folded into
      // it: that would read as "the pin is already gone".
      setMessage({
        tone: 'error',
        text:
          error instanceof ApiError && error.status === 404
            ? t('No such binding')
            : getServerErrorMessage(error, t('Purge failed')),
      })
    },
  })

  const purgeAll = useMutation({
    mutationFn: purgeAllAffinity,
    meta: { errorToast: false },
    onSuccess: async (result) => {
      const purged = result?.purged ?? 0
      if (result?.complete) {
        toast.success(t('Purged {{purged}} binding(s)', { purged }))
      } else {
        // The scan hit its round cap: say so rather than report a full wipe.
        toast.warning(
          t('Purged {{purged}} binding(s). Incomplete, some may remain.', {
            purged,
          })
        )
      }
      setPurgeAllOpen(false)
      await refetchAll()
    },
    // The dialog stays open so the operator can retry.
    onError: (error) =>
      toast.error(getServerErrorMessage(error, t('Purge failed'))),
  })

  const submitKey = () => {
    const trimmed = key.trim()
    if (!trimmed || purgeOne.isPending) return
    setMessage(null)
    purgeOne.mutate(trimmed)
  }

  const stats = props.snapshot?.stats
  const yesNo = (v: boolean) => (v ? t('Yes') : t('No'))

  return (
    <Card data-testid='diag-affinity-panel'>
      <CardHeader>
        <CardTitle>{t('Session affinity')}</CardTitle>
        <CardDescription>
          {t('Sticky routing bindings and how often they were honoured.')}
        </CardDescription>
      </CardHeader>
      <CardContent className='flex flex-col gap-4'>
        {props.failed || !stats ? (
          <div data-testid='diag-affinity-unavailable'>
            <ErrorState
              className='min-h-[140px]'
              title={t('Could not load session-affinity stats')}
              onRetry={props.onRetry}
            />
          </div>
        ) : (
          <>
            <div className='grid grid-cols-2 gap-4 sm:grid-cols-4'>
              <Stat label={t('Backend')} testId='diag-affinity-backend'>
                {stats.backend}
              </Stat>
              <Stat label={t('Enabled')} testId='diag-affinity-enabled'>
                {yesNo(stats.enabled)}
              </Stat>
              <Stat label={t('TTL (s)')} testId='diag-affinity-ttl'>
                {stats.ttl_seconds}
              </Stat>
              {stats.backend === 'memory' ? (
                <Stat
                  label={t('In-memory entries')}
                  testId='diag-affinity-mementries'
                >
                  {stats.mem_entries}
                </Stat>
              ) : (
                <div className='min-w-0'>
                  <div className='text-muted-foreground text-xs'>
                    {t('In-memory entries')}
                  </div>
                  <div
                    className='text-muted-foreground mt-0.5 text-xs'
                    data-testid='diag-affinity-mementries-note'
                  >
                    {t(
                      'In-process fallback map, reads 0 while the backend is redis'
                    )}
                  </div>
                </div>
              )}
              <Stat label={t('Hits')} testId='diag-affinity-hit'>
                {stats.hit}
              </Stat>
              <Stat label={t('Misses')} testId='diag-affinity-miss'>
                {stats.miss}
              </Stat>
              <Stat label={t('Stale')} testId='diag-affinity-stale'>
                {stats.stale}
              </Stat>
            </div>
            <p
              className='text-muted-foreground text-xs leading-relaxed'
              data-testid='diag-affinity-scope-note'
            >
              {t(
                'Scope: {{scope}}. The counters are per-replica; a reload can land on a different replica and show different numbers.',
                { scope: props.snapshot?.scope ?? '-' }
              )}
            </p>
          </>
        )}

        <form
          className='flex flex-wrap items-center gap-2 border-t pt-4'
          onSubmit={(e) => {
            e.preventDefault()
            submitKey()
          }}
        >
          <Input
            className='max-w-xs min-w-48 flex-1 font-mono'
            aria-label={t('Affinity key')}
            placeholder={t('Affinity key')}
            autoComplete='off'
            value={key}
            onChange={(e) => setKey(e.target.value)}
            data-testid='diag-affinity-purge-key-input'
          />
          <Button
            type='submit'
            variant='outline'
            disabled={purgeOne.isPending}
            data-testid='diag-affinity-purge-key-btn'
          >
            <Eraser className='size-4' />
            {t('Purge one')}
          </Button>
          <Button
            type='button'
            variant='destructive'
            onClick={() => setPurgeAllOpen(true)}
            data-testid='diag-affinity-purge-all-btn'
          >
            <Trash2 className='size-4' />
            {t('Purge all')}
          </Button>
          {message && (
            <StatusBadge
              label={message.text}
              variant={message.tone === 'ok' ? 'success' : 'danger'}
              copyable={false}
              className='max-w-full'
              role='status'
              data-testid='diag-affinity-purge-key-msg'
            />
          )}
        </form>
      </CardContent>

      <PurgeAllDialog
        open={purgeAllOpen}
        pending={purgeAll.isPending}
        onClose={() => setPurgeAllOpen(false)}
        onConfirm={() => purgeAll.mutate()}
      />
    </Card>
  )
}
