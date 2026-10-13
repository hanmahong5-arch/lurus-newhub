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
import { useCallback, useEffect, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { StepUpCancelledError, useStepUp } from '@/components/step-up'
import { handleServerError } from '@/lib/handle-server-error'

import { getChannelKey, requireServerSuccess } from '../api'

/**
 * Reveal a stored channel key (root only). The endpoint sits behind the
 * server's step-up gate, so the request runs through useStepUp, which prompts
 * for a verification code and retries once. Render `stepUpDialog` in the tree.
 */
export function useChannelKeyDisclosure(
  open: boolean,
  channelId: number | null
) {
  const { t } = useTranslation()
  const { run, dialog: stepUpDialog } = useStepUp()
  const [disclosedKey, setDisclosedKey] = useState<{
    channelId: number
    key: string
  } | null>(null)
  const [isChannelKeyLoading, setIsChannelKeyLoading] = useState(false)
  const operation = useRef<symbol | null>(null)

  useEffect(() => {
    setDisclosedKey(null)
    setIsChannelKeyLoading(false)
    return () => {
      operation.current = null
    }
  }, [open, channelId])

  const handleRevealKey = useCallback(async () => {
    if (!channelId || !open || operation.current) return
    const current = Symbol('reveal')
    operation.current = current
    setIsChannelKeyLoading(true)
    try {
      const res = requireServerSuccess(
        await run(() => getChannelKey(channelId)),
        t('Failed to fetch channel key')
      )
      if (operation.current !== current) return
      setDisclosedKey({ channelId, key: res.data?.key ?? '' })
      toast.success(t('Channel key unlocked'))
    } catch (error) {
      if (operation.current === current && !(error instanceof StepUpCancelledError)) {
        handleServerError(error, t('Failed to fetch channel key'))
      }
    } finally {
      if (operation.current === current) {
        operation.current = null
        setIsChannelKeyLoading(false)
      }
    }
  }, [channelId, open, run, t])

  const channelKey =
    open && disclosedKey?.channelId === channelId ? disclosedKey.key : null
  return { channelKey, isChannelKeyLoading, handleRevealKey, stepUpDialog }
}
