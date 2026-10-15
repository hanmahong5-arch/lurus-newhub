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
import {
  useMutation,
  useQuery,
  useQueryClient,
  type UseQueryResult,
} from '@tanstack/react-query'
import type { TFunction } from 'i18next'
import {
  useMemo,
  useRef,
  useState,
  type FormEvent,
  type ReactNode,
} from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { ConfirmDialog } from '@/components/confirm-dialog'
import { CopyButton } from '@/components/copy-button'
import {
  SideDrawerSection,
  sideDrawerContentClassName,
  sideDrawerFormClassName,
  sideDrawerHeaderClassName,
} from '@/components/drawer-layout'
import { formatTimestampToDate } from '@/components/format'
import { LoadingState } from '@/components/loading-state'
import { StatusBadge, type StatusVariant } from '@/components/status-badge'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import {
  Sheet,
  SheetContent,
  SheetDescription,
  SheetHeader,
  SheetTitle,
} from '@/components/ui/sheet'
import { handleServerError } from '@/lib/handle-server-error'

import { issueInvite, listInvites, revokeInvite, tenantsQueryKey } from '../api'
import {
  INVITE_DEFAULT_TTL_HOURS,
  INVITE_MAX_TTL_HOURS,
} from '../constants'
import {
  inviteDisplayStatus,
  inviteLink,
  isRevocable,
  parseTtlHours,
  type InviteStatusName,
} from '../lib/invite'
import { formatIsoDate } from '../lib/labels'
import type { Tenant, TenantInvite, TenantInviteList } from '../types'
import { Field } from './ui-bits'
import { useTenants } from './tenants-provider'

const STATUS_VARIANT: Record<InviteStatusName | 'expired', StatusVariant> = {
  pending: 'info',
  consumed: 'success',
  revoked: 'warning',
  expired: 'neutral',
  unknown: 'neutral',
}

function inviteStatusText(
  status: InviteStatusName | 'expired',
  t: TFunction
): string {
  switch (status) {
    case 'pending':
      return t('Pending')
    case 'consumed':
      return t('Consumed')
    case 'revoked':
      return t('Revoked')
    case 'expired':
      return t('Expired')
    default:
      return t('Unknown')
  }
}

function InviteList(props: {
  query: UseQueryResult<TenantInviteList>
  revoking: boolean
  onRevoke: (invite: TenantInvite) => void
}) {
  const { t } = useTranslation()
  const { query, revoking, onRevoke } = props
  const invites = query.data?.invites ?? []

  let body: ReactNode
  if (query.isLoading) {
    body = <LoadingState inline size='sm' message={t('Loading...')} />
  } else if (query.isError) {
    // A failed read is not "no invites yet".
    body = (
      <div
        className='text-muted-foreground flex items-center gap-3 text-sm'
        data-testid='invite-list-load-failed'
      >
        <span>{t('Could not load invites.')}</span>
        <Button
          size='sm'
          variant='outline'
          onClick={() => void query.refetch()}
        >
          {t('Retry')}
        </Button>
      </div>
    )
  } else if (invites.length === 0) {
    body = (
      <p className='text-muted-foreground text-sm'>
        {t('No invites issued yet.')}
      </p>
    )
  } else {
    body = (
      <div className='border-border overflow-x-auto rounded-lg border'>
        <table className='w-full text-sm'>
          <thead>
            <tr className='text-muted-foreground border-b text-left text-xs'>
              <th className='px-3 py-2 font-medium'>{t('Code')}</th>
              <th className='px-3 py-2 font-medium'>{t('Status')}</th>
              <th className='px-3 py-2 font-medium'>{t('Expires')}</th>
              <th className='px-3 py-2 font-medium'>{t('Consumed by')}</th>
              <th className='px-3 py-2 font-medium'>{t('Created')}</th>
              <th className='px-3 py-2' />
            </tr>
          </thead>
          <tbody>
            {invites.map((invite) => {
              const status = inviteDisplayStatus(invite)
              return (
                <tr
                  key={invite.id}
                  className='border-b last:border-b-0'
                  data-testid={`invite-row-${invite.id}`}
                >
                  <td className='px-3 py-2 font-mono'>{invite.code_prefix}…</td>
                  <td className='px-3 py-2'>
                    <StatusBadge
                      label={inviteStatusText(status, t)}
                      variant={STATUS_VARIANT[status]}
                      copyable={false}
                    />
                  </td>
                  <td className='px-3 py-2 font-mono text-xs whitespace-nowrap'>
                    {invite.expired_time
                      ? formatTimestampToDate(invite.expired_time)
                      : t('Never')}
                  </td>
                  <td className='px-3 py-2 font-mono'>
                    {invite.consumed_by_account_id ?? '-'}
                  </td>
                  <td className='px-3 py-2 font-mono text-xs whitespace-nowrap'>
                    {formatIsoDate(invite.created_at)}
                  </td>
                  <td className='px-3 py-2 text-right'>
                    {isRevocable(invite) && (
                      <Button
                        size='sm'
                        variant='ghost'
                        disabled={revoking}
                        onClick={() => onRevoke(invite)}
                        data-testid={`invite-revoke-btn-${invite.id}`}
                      >
                        {t('Revoke')}
                      </Button>
                    )}
                  </td>
                </tr>
              )
            })}
          </tbody>
        </table>
      </div>
    )
  }

  return (
    <section className='grid gap-2' data-testid='invite-list'>
      <h3 className='text-sm font-semibold'>{t('Issued invites')}</h3>
      {body}
      {query.data && query.data.total > invites.length && (
        <p className='text-muted-foreground text-xs'>
          {t('Showing the latest {{shown}} of {{total}} invites.', {
            shown: invites.length,
            total: query.data.total,
          })}
        </p>
      )}
    </section>
  )
}

export function TenantInvitesSheet({ tenant }: { tenant: Tenant }) {
  const { t } = useTranslation()
  const { setDialog } = useTenants()
  const qc = useQueryClient()
  const listKey = useMemo(
    () => [...tenantsQueryKey, 'invites', tenant.id],
    [tenant.id]
  )

  const [ttl, setTtl] = useState(String(INVITE_DEFAULT_TTL_HOURS))
  const [ttlError, setTtlError] = useState<string | undefined>()
  // The plaintext link lives only here, for the life of this drawer: the list
  // endpoint returns the code prefix and never the code again.
  const [issuedLink, setIssuedLink] = useState<string | null>(null)
  const [revokeTarget, setRevokeTarget] = useState<TenantInvite | null>(null)
  const dismissRef = useRef<HTMLButtonElement>(null)

  const list = useQuery({
    queryKey: listKey,
    queryFn: () => listInvites(tenant.id),
    // The failure is shown in place; no second toast on top of it.
    meta: { errorToast: false },
  })

  const issue = useMutation({
    mutationFn: (hours: number) => issueInvite(tenant.id, hours),
    onSuccess: (invite) => {
      setIssuedLink(inviteLink(window.location.origin, invite.code))
      toast.success(t('Invite issued'))
      void qc.invalidateQueries({ queryKey: listKey })
    },
    onError: (error) => handleServerError(error, t('Failed to issue invite')),
  })

  const revoke = useMutation({
    mutationFn: (invite: TenantInvite) => revokeInvite(tenant.id, invite.id),
    onSuccess: () => {
      toast.success(t('Invite revoked'))
      setRevokeTarget(null)
      void qc.invalidateQueries({ queryKey: listKey })
    },
    onError: (error) => handleServerError(error, t('Failed to revoke invite')),
  })

  const busy = issue.isPending || revoke.isPending

  const submit = (e: FormEvent) => {
    e.preventDefault()
    if (issue.isPending) return
    const parsed = parseTtlHours(ttl)
    if (!parsed.ok) {
      setTtlError(
        parsed.error === 'too-long'
          ? t('The validity can be at most {{max}} hours (30 days).', {
              max: INVITE_MAX_TTL_HOURS,
            })
          : t('Enter a whole number of hours.')
      )
      return
    }
    setTtlError(undefined)
    issue.mutate(parsed.hours)
  }

  // While the link is on screen, Escape, the X and the backdrop must not throw
  // it away: closing loses the code for good. They steer focus to the explicit
  // dismiss next to the link instead, so the notice has to be acted on.
  const requestClose = (open: boolean) => {
    if (open) return
    if (issuedLink) {
      dismissRef.current?.focus()
      return
    }
    if (!busy) setDialog(null)
  }

  return (
    <>
      <Sheet open onOpenChange={requestClose}>
        <SheetContent
          className={sideDrawerContentClassName('max-w-none sm:!max-w-[680px]')}
        >
          <SheetHeader className={sideDrawerHeaderClassName()}>
            <SheetTitle>
              {t('{{name}} · invites', { name: tenant.name })}
            </SheetTitle>
            <SheetDescription>
              {t('One-time codes that bring a new person into this tenant.')}
            </SheetDescription>
          </SheetHeader>

          <div className={sideDrawerFormClassName('gap-6')}>
            <SideDrawerSection>
              <form
                onSubmit={submit}
                noValidate
                className='grid gap-3'
                data-testid='invite-issue-form'
              >
                <h3 className='text-sm font-semibold'>{t('Issue invite')}</h3>
                <Field
                  label={t('Validity (hours)')}
                  htmlFor='invite-ttl-hours'
                  hint={t(
                    'Blank uses the default of {{hours}} hours; the longest is {{max}}.',
                    {
                      hours: INVITE_DEFAULT_TTL_HOURS,
                      max: INVITE_MAX_TTL_HOURS,
                    }
                  )}
                  error={ttlError}
                >
                  <Input
                    id='invite-ttl-hours'
                    data-testid='invite-ttl-hours'
                    inputMode='numeric'
                    value={ttl}
                    onChange={(e) => {
                      setTtl(e.target.value)
                      setTtlError(undefined)
                    }}
                    disabled={issue.isPending}
                  />
                </Field>
                <div>
                  <Button
                    type='submit'
                    disabled={issue.isPending}
                    data-testid='invite-issue-submit'
                  >
                    {issue.isPending ? t('Issuing...') : t('Issue invite')}
                  </Button>
                </div>
              </form>
            </SideDrawerSection>

            {issuedLink && (
              <section
                className='border-warning/50 bg-warning/5 grid gap-2 rounded-lg border p-4'
                data-testid='invite-issued-box'
              >
                <div className='text-sm font-semibold'>
                  {t('One-time invite link')}
                </div>
                <div className='flex items-center gap-2'>
                  <Input
                    readOnly
                    value={issuedLink}
                    className='font-mono text-xs'
                    data-testid='invite-issued-link'
                    aria-label={t('One-time invite link')}
                    onFocus={(e) => e.target.select()}
                  />
                  <CopyButton
                    value={issuedLink}
                    variant='outline'
                    aria-label={t('Copy invite link')}
                  />
                  <Button
                    ref={dismissRef}
                    type='button'
                    variant='outline'
                    onClick={() => setDialog(null)}
                    data-testid='invite-issued-dismiss'
                  >
                    {t('Done')}
                  </Button>
                </div>
                <p className='text-muted-foreground text-xs'>
                  {t(
                    'Shown once — copy this link now. It will not be shown again.'
                  )}
                </p>
              </section>
            )}

            <InviteList
              query={list}
              revoking={revoke.isPending}
              onRevoke={setRevokeTarget}
            />
          </div>
        </SheetContent>
      </Sheet>

      <ConfirmDialog
        destructive
        open={revokeTarget !== null}
        onOpenChange={(open) => {
          if (!open && !revoke.isPending) setRevokeTarget(null)
        }}
        title={t('Revoke this invite?')}
        desc={t(
          'The code {{code}}… stops working at once. This cannot be undone.',
          { code: revokeTarget?.code_prefix ?? '' }
        )}
        confirmText={revoke.isPending ? t('Revoking...') : t('Revoke')}
        isLoading={revoke.isPending}
        handleConfirm={() => {
          if (revokeTarget && !revoke.isPending) revoke.mutate(revokeTarget)
        }}
      />
    </>
  )
}
