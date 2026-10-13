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
import { Trash2 } from 'lucide-react'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { ConfirmDialog } from '@/components/confirm-dialog'
import { DateTimePicker } from '@/components/datetime-picker'
import { formatDateTimeStr } from '@/components/format'
import { Button } from '@/components/ui/button'
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from '@/components/ui/card'
import { handleServerError } from '@/lib/handle-server-error'

import { deleteLogsBefore } from '../api'

/**
 * Delete usage logs older than a chosen moment. The server deletes in batches
 * and answers with the number of rows removed; a refusal is shown as an error,
 * never as "0 removed".
 */
export function LogCleanupCard() {
  const { t } = useTranslation()
  const [cutoff, setCutoff] = useState<Date | undefined>(undefined)
  const [confirming, setConfirming] = useState(false)

  const cleanup = useMutation({
    mutationFn: (seconds: number) => deleteLogsBefore(seconds),
    onSuccess: (count) => {
      setConfirming(false)
      toast.success(
        count > 0
          ? t('{{count}} log entries removed.', { count })
          : t('No log entries matched the selected time.')
      )
    },
    onError: (error) => {
      setConfirming(false)
      handleServerError(error, t('Failed to clean logs'))
    },
  })

  const seconds = cutoff ? Math.floor(cutoff.getTime() / 1000) : null

  return (
    <Card data-settings-form-span='full'>
      <CardHeader>
        <CardTitle>{t('Clean history logs')}</CardTitle>
        <CardDescription>
          {t(
            'Permanently delete every log entry created before the selected time.'
          )}
        </CardDescription>
      </CardHeader>
      <CardContent className='flex flex-wrap items-center gap-3'>
        <DateTimePicker
          value={cutoff}
          onChange={setCutoff}
          placeholder={t('Select a time')}
        />
        <Button
          type='button'
          variant='destructive'
          disabled={seconds === null || cleanup.isPending}
          onClick={() => {
            if (seconds === null) {
              toast.error(t('Select a timestamp before clearing logs.'))
              return
            }
            setConfirming(true)
          }}
        >
          <Trash2 data-icon='inline-start' />
          {t('Clean logs')}
        </Button>
      </CardContent>
      <ConfirmDialog
        open={confirming}
        onOpenChange={setConfirming}
        title={t('Confirm log cleanup')}
        desc={t(
          'All log entries before {{time}} will be deleted. This action cannot be undone.',
          { time: cutoff ? formatDateTimeStr(cutoff) : '' }
        )}
        confirmText={t('Delete logs')}
        destructive
        isLoading={cleanup.isPending}
        handleConfirm={() => {
          if (seconds !== null) cleanup.mutate(seconds)
        }}
      />
    </Card>
  )
}
