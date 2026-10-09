import {
  keepPreviousData,
  useMutation,
  useQuery,
  useQueryClient,
} from '@tanstack/react-query'
import { Plus, UserCog, Users } from 'lucide-react'
import { useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { ConfirmDialog } from '@/components/confirm-dialog'
import { EmptyState } from '@/components/empty-state'
import { ErrorState } from '@/components/error-state'
import { PageHeader } from '@/components/layout/page-header'
import { LoadingState } from '@/components/loading-state'
import { Button } from '@/components/ui/button'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { toApiError } from '@/lib/api'
import { currentUserQueryOptions, userAccess } from '@/lib/user'

import {
  PAGE_SIZE,
  createInvite,
  invitesQueryOptions,
  membersQueryKey,
  membersQueryOptions,
  projectsQueryOptions,
  revokeInvite,
  setMemberRole,
} from './api'
import { InviteFormDialog } from './components/invite-form-dialog'
import { InviteLinkDialog } from './components/invite-link-dialog'
import { InvitesTable } from './components/invites-table'
import { MembersTable } from './components/members-table'
import { RedeemCard } from './components/redeem-card'
import { RoleDialog } from './components/role-dialog'
import {
  inviteErrorMessage,
  roleErrorMessage,
} from './lib/errors'
import { inviteLink, role as toRole, type SelfRef } from './lib/map'
import type {
  Invite,
  InviteWriteBody,
  IssuedInvite,
  Member,
  MemberRole,
} from './types'

type Dialog =
  | { kind: 'role'; member: Member | null }
  | { kind: 'invite' }
  | { kind: 'link'; issued: IssuedInvite }
  | { kind: 'revoke'; invite: Invite }
  | null

export function OrgMembersPage() {
  const { t } = useTranslation()
  const qc = useQueryClient()
  const me = useQuery(currentUserQueryOptions)
  const access = userAccess(me.data)
  const canManage = access.isTenantAdmin

  const self = useMemo<SelfRef | null>(
    () =>
      me.data
        ? {
            id: me.data.id,
            username: me.data.username,
            displayName: me.data.display_name ?? '',
            email: me.data.email ?? '',
            role: toRole(me.data.tenant_role),
            isPayer: me.data.is_payer === true,
          }
        : null,
    [me.data]
  )

  const [page, setPage] = useState(1)
  const [dialog, setDialog] = useState<Dialog>(null)
  const [dialogError, setDialogError] = useState<string | null>(null)
  // Full invite codes exist only in memory, from the create response.
  const [codes, setCodes] = useState<Record<number, string>>({})

  const members = useQuery({
    ...membersQueryOptions(self),
    enabled: canManage && self !== null,
  })
  const invites = useQuery({
    ...invitesQueryOptions(page),
    enabled: canManage,
    placeholderData: keepPreviousData,
  })
  const projects = useQuery({ ...projectsQueryOptions, enabled: canManage })

  const refresh = () => qc.invalidateQueries({ queryKey: membersQueryKey })
  const closeDialog = () => {
    setDialog(null)
    setDialogError(null)
  }

  const changeRole = useMutation({
    mutationFn: (v: { userId: number; role: MemberRole }) =>
      setMemberRole(v.userId, v.role),
    onSuccess: async () => {
      toast.success(t('Role updated'))
      closeDialog()
      await refresh()
    },
    onError: (e) => setDialogError(roleErrorMessage(e, t)),
  })

  const create = useMutation({
    mutationFn: (body: InviteWriteBody) => createInvite(body),
    onSuccess: async (issued) => {
      setCodes((c) => ({ ...c, [issued.id]: issued.code }))
      setDialogError(null)
      setDialog({ kind: 'link', issued })
      await refresh()
    },
    onError: (e) => setDialogError(inviteErrorMessage(e, t)),
  })

  const revoke = useMutation({
    mutationFn: (invite: Invite) => revokeInvite(invite.id),
    onSuccess: async () => {
      toast.success(t('Invitation revoked'))
      closeDialog()
      await refresh()
    },
    onError: (e) => {
      toast.error(inviteErrorMessage(e, t))
      closeDialog()
    },
  })

  const links = useMemo(() => {
    const origin = typeof window === 'undefined' ? '' : window.location.origin
    const out: Record<number, string> = {}
    for (const [id, code] of Object.entries(codes)) {
      out[Number(id)] = inviteLink(origin, code)
    }
    return out
  }, [codes])

  const origin = typeof window === 'undefined' ? '' : window.location.origin

  let membersBody
  if (me.isError) {
    membersBody = (
      <ErrorState
        title={t('Could not load your account')}
        description={toApiError(me.error).message}
        onRetry={() => void me.refetch()}
      />
    )
  } else if (members.isPending) {
    membersBody = <LoadingState />
  } else if (members.isError) {
    membersBody = (
      <ErrorState
        title={t('Could not load members')}
        description={toApiError(members.error).message}
        onRetry={() => void members.refetch()}
      />
    )
  } else if (members.data.length === 0) {
    membersBody = (
      <EmptyState
        icon={Users}
        title={t('No members yet')}
        description={t('Invite people to your organization.')}
        bordered
      />
    )
  } else {
    membersBody = (
      <div className='grid min-w-0 gap-2'>
        <div className='min-w-0 rounded-lg border'>
          <MembersTable
            members={members.data}
            selfId={me.data?.id ?? 0}
            canEdit
            onEditRole={(m) => setDialog({ kind: 'role', member: m })}
          />
        </div>
        <p className='text-muted-foreground text-xs'>
          {t(
            'Lists the members of your departments and you. People who belong to no department are not listed; change their role by user ID.'
          )}
        </p>
      </div>
    )
  }

  const inviteList = invites.data
  const pageCount = Math.max(1, Math.ceil((inviteList?.total ?? 0) / PAGE_SIZE))
  let invitesBody
  if (invites.isPending) {
    invitesBody = <LoadingState />
  } else if (invites.isError && inviteList === undefined) {
    invitesBody = (
      <ErrorState
        title={t('Could not load invitations')}
        description={toApiError(invites.error).message}
        onRetry={() => void invites.refetch()}
      />
    )
  } else if (inviteList && inviteList.items.length === 0 && page === 1) {
    invitesBody = (
      <EmptyState
        icon={UserCog}
        title={t('No invitations yet')}
        description={t('Create an invitation to bring someone in.')}
        bordered
      />
    )
  } else if (inviteList) {
    invitesBody = (
      <div className='grid min-w-0 gap-3'>
        <div className='min-w-0 rounded-lg border'>
          <InvitesTable
            invites={inviteList.items}
            projects={projects.data ?? []}
            links={links}
            onRevoke={(invite) => setDialog({ kind: 'revoke', invite })}
          />
        </div>
        <div className='flex items-center justify-end gap-2 text-sm'>
          <Button
            variant='outline'
            size='sm'
            disabled={page <= 1}
            onClick={() => setPage(Math.max(1, page - 1))}
          >
            {t('Previous')}
          </Button>
          <span>
            {t('Page {{page}} of {{pages}}', { page, pages: pageCount })}
          </span>
          <Button
            variant='outline'
            size='sm'
            disabled={page >= pageCount}
            onClick={() => setPage(page + 1)}
          >
            {t('Next')}
          </Button>
        </div>
        {invites.isError && (
          <p role='alert' className='text-destructive text-sm'>
            {t('Refreshing the list failed: {{message}}', {
              message: toApiError(invites.error).message,
            })}
          </p>
        )}
      </div>
    )
  }

  if (me.isPending) return <LoadingState />

  const roleMember = dialog?.kind === 'role' ? dialog.member : null
  const revokeTarget = dialog?.kind === 'revoke' ? dialog.invite : null

  return (
    <>
      <PageHeader
        title={t('Members & Invitations')}
        description={t('Manage who belongs to your organization and what they can do.')}
      />

      <Tabs defaultValue={canManage ? 'members' : 'redeem'}>
        <TabsList>
          {canManage && (
            <TabsTrigger value='members'>{t('Members')}</TabsTrigger>
          )}
          {canManage && (
            <TabsTrigger value='invites'>{t('Invitations')}</TabsTrigger>
          )}
          <TabsTrigger value='redeem'>{t('Redeem code')}</TabsTrigger>
        </TabsList>

        {canManage && (
          <TabsContent value='members' className='grid gap-3'>
            <div className='flex justify-end'>
              <Button
                variant='outline'
                onClick={() => setDialog({ kind: 'role', member: null })}
              >
                {t('Set role by user ID')}
              </Button>
            </div>
            {membersBody}
          </TabsContent>
        )}
        {canManage && (
          <TabsContent value='invites' className='grid gap-3'>
            <div className='flex justify-end'>
              <Button
                onClick={() => {
                  setDialogError(null)
                  setDialog({ kind: 'invite' })
                }}
              >
                <Plus />
                {t('Create invitation')}
              </Button>
            </div>
            {invitesBody}
          </TabsContent>
        )}
        <TabsContent value='redeem'>
          <RedeemCard onRedeemed={() => void qc.invalidateQueries()} />
        </TabsContent>
      </Tabs>

      {dialog?.kind === 'role' && (
        <RoleDialog
          key={roleMember?.userId ?? 'by-id'}
          member={roleMember ?? undefined}
          saving={changeRole.isPending}
          error={dialogError}
          onSubmit={(userId, role) => {
            setDialogError(null)
            changeRole.mutate({ userId, role })
          }}
          onClose={closeDialog}
        />
      )}
      {dialog?.kind === 'invite' && (
        <InviteFormDialog
          projects={projects.data ?? []}
          projectsFailed={projects.isError}
          saving={create.isPending}
          error={dialogError}
          onSubmit={(b) => {
            setDialogError(null)
            create.mutate(b)
          }}
          onClose={closeDialog}
        />
      )}
      {dialog?.kind === 'link' && (
        <InviteLinkDialog
          code={dialog.issued.code}
          link={inviteLink(origin, dialog.issued.code)}
          onClose={closeDialog}
        />
      )}
      <ConfirmDialog
        open={revokeTarget !== null}
        onOpenChange={(open) => {
          if (!open) closeDialog()
        }}
        title={t('Revoke this invitation?')}
        desc={t('The code stops working immediately. This cannot be undone.')}
        confirmText={t('Revoke')}
        destructive
        isLoading={revoke.isPending}
        handleConfirm={() => revokeTarget && revoke.mutate(revokeTarget)}
      />
    </>
  )
}
