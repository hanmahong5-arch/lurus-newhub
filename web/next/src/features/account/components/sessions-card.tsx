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
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Laptop, LogOut, MonitorSmartphone } from 'lucide-react'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { formatTimestampToDate } from '@/components/format'
import { ConfirmDialog } from '@/components/confirm-dialog'
import { ErrorState } from '@/components/error-state'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import {
  Card,
  CardAction,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from '@/components/ui/card'
import {
  Empty,
  EmptyDescription,
  EmptyHeader,
  EmptyMedia,
  EmptyTitle,
} from '@/components/ui/empty'
import { Separator } from '@/components/ui/separator'
import { Skeleton } from '@/components/ui/skeleton'
import { toApiError } from '@/lib/api'
import { signOut } from '@/lib/session'

import {
  accountKeys,
  fetchSessions,
  revokeOtherSessions,
  revokeSession,
} from '../api'
import {
  authMethodLabel,
  revocableId,
  sessionDeviceLabel,
} from '../lib/sessions'
import type { LoginSession } from '../types'

function SessionRow(props: {
  session: LoginSession
  onRevoke: (session: LoginSession) => void
}) {
  const { t } = useTranslation()
  const { session } = props
  const canRevoke = session.current || revocableId(session) !== null
  return (
    <div className='flex flex-col gap-3 py-4 sm:flex-row sm:items-center'>
      <div className='bg-muted flex size-10 shrink-0 items-center justify-center rounded-lg'>
        <Laptop className='size-5' />
      </div>
      <div className='min-w-0 flex-1'>
        <div className='flex flex-wrap items-center gap-2'>
          <p className='font-medium'>{sessionDeviceLabel(session, t)}</p>
          {session.current && <Badge variant='secondary'>{t('Current')}</Badge>}
        </div>
        <p className='text-muted-foreground mt-1 text-xs'>
          {t('IP: {{ip}} · Method: {{method}}', {
            ip: session.ip || t('Unknown'),
            method: authMethodLabel(session.auth_method, t),
          })}
        </p>
        <p className='text-muted-foreground mt-1 text-xs'>
          {t('Signed in {{created}} · Last active {{seen}}', {
            created: formatTimestampToDate(session.created_at),
            seen: formatTimestampToDate(session.last_seen_at),
          })}
        </p>
      </div>
      {canRevoke && (
        <Button
          type='button'
          variant={session.current ? 'outline' : 'ghost'}
          size='sm'
          onClick={() => props.onRevoke(session)}
        >
          {session.current ? t('Sign out') : t('Revoke')}
        </Button>
      )}
    </div>
  )
}

export function SessionsCard() {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const [target, setTarget] = useState<LoginSession | null>(null)
  const [confirmOthers, setConfirmOthers] = useState(false)

  const sessions = useQuery({
    queryKey: accountKeys.sessions,
    queryFn: fetchSessions,
    retry: false,
  })

  const revoke = useMutation({
    mutationFn: (id: number) => revokeSession(id),
    onSuccess: async () => {
      setTarget(null)
      toast.success(t('Session signed out'))
      await queryClient.invalidateQueries({ queryKey: accountKeys.sessions })
    },
    onError: (error) => {
      setTarget(null)
      toast.error(toApiError(error).message || t('Failed to sign out session'))
    },
  })

  const revokeOthers = useMutation({
    mutationFn: revokeOtherSessions,
    onSuccess: async () => {
      setConfirmOthers(false)
      toast.success(t('Other sessions signed out'))
      await queryClient.invalidateQueries({ queryKey: accountKeys.sessions })
    },
    onError: (error) => {
      setConfirmOthers(false)
      toast.error(
        toApiError(error).message || t('Failed to sign out other sessions')
      )
    },
  })

  const list = sessions.data
  const items = list?.items ?? []
  const hasOthers = items.some((s) => !s.current && revocableId(s) !== null)

  let content: React.ReactNode
  if (sessions.isPending) {
    content = (
      <div role='status' aria-label={t('Loading...')} className='space-y-3'>
        <Skeleton className='h-20 w-full' />
        <Skeleton className='h-20 w-full' />
      </div>
    )
  } else if (sessions.isError) {
    content = (
      <ErrorState
        className='min-h-0 py-6'
        title={t('Unable to load login sessions')}
        description={toApiError(sessions.error).message}
        onRetry={() => void sessions.refetch()}
      />
    )
  } else if (!list?.registry_enabled) {
    content = (
      <Empty>
        <EmptyHeader>
          <EmptyMedia variant='icon'>
            <MonitorSmartphone />
          </EmptyMedia>
          <EmptyTitle>{t('Per-device sessions are not tracked')}</EmptyTitle>
          <EmptyDescription>
            {t(
              'This deployment does not keep a list of signed-in devices, so there is nothing to review or revoke here. You can still sign out of this browser.'
            )}
          </EmptyDescription>
        </EmptyHeader>
      </Empty>
    )
  } else if (items.length === 0) {
    content = (
      <Empty>
        <EmptyHeader>
          <EmptyMedia variant='icon'>
            <MonitorSmartphone />
          </EmptyMedia>
          <EmptyTitle>{t('No active login sessions')}</EmptyTitle>
        </EmptyHeader>
      </Empty>
    )
  } else {
    content = (
      <div className='flex flex-col'>
        {items.map((session, index) => (
          <div key={String(session.id)}>
            {index > 0 && <Separator />}
            <SessionRow session={session} onRevoke={setTarget} />
          </div>
        ))}
      </div>
    )
  }

  return (
    <>
      <Card data-card-hover='false'>
        <CardHeader>
          <CardTitle>{t('Login sessions')}</CardTitle>
          <CardDescription>
            {t('Review and sign out devices currently using your account.')}
          </CardDescription>
          <CardAction>
            <Button
              type='button'
              variant='outline'
              size='sm'
              disabled={!hasOthers || revokeOthers.isPending}
              onClick={() => setConfirmOthers(true)}
            >
              <LogOut className='size-4' />
              {t('Sign out other sessions')}
            </Button>
          </CardAction>
        </CardHeader>
        <CardContent>{content}</CardContent>
      </Card>

      <ConfirmDialog
        open={target !== null}
        onOpenChange={(open) => !open && setTarget(null)}
        title={
          target?.current ? t('Sign out this device?') : t('Revoke session?')
        }
        desc={t(
          'This session will lose access immediately and must sign in again.'
        )}
        confirmText={target?.current ? t('Sign out') : t('Revoke')}
        destructive
        isLoading={revoke.isPending}
        handleConfirm={() => {
          if (!target) return
          if (target.current) {
            void signOut()
            return
          }
          const id = revocableId(target)
          if (id !== null) revoke.mutate(id)
        }}
      />

      <ConfirmDialog
        open={confirmOthers}
        onOpenChange={setConfirmOthers}
        title={t('Sign out other sessions?')}
        desc={t(
          'Every other device will lose access immediately. This device will remain signed in.'
        )}
        confirmText={t('Sign out others')}
        destructive
        isLoading={revokeOthers.isPending}
        handleConfirm={() => revokeOthers.mutate()}
      />
    </>
  )
}
