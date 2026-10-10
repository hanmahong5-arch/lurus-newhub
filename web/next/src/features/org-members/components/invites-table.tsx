import { useTranslation } from 'react-i18next'

import { CopyButton } from '@/components/copy-button'
import { StatusBadge, type StatusVariant } from '@/components/status-badge'
import { Button } from '@/components/ui/button'
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table'
import { formatDate } from '@/lib/time'

import type { Invite, InviteState, ProjectRef } from '../types'
import { useRoleLabel } from '../lib/labels'

const STATE_VARIANT: Record<InviteState, StatusVariant> = {
  active: 'success',
  used: 'neutral',
  revoked: 'danger',
  expired: 'warning',
}

interface InvitesTableProps {
  invites: Invite[]
  projects: ProjectRef[]
  /** Full codes known in this session (create only), by invite id. */
  links: Record<number, string>
  onRevoke: (invite: Invite) => void
}

export function InvitesTable(props: InvitesTableProps) {
  const { t } = useTranslation()
  const roleLabel = useRoleLabel()
  const stateLabel: Record<InviteState, string> = {
    active: t('Active'),
    used: t('Used'),
    revoked: t('Revoked'),
    expired: t('Expired'),
  }
  const projectName = (id: number) =>
    id <= 0
      ? '--'
      : (props.projects.find((p) => p.id === id)?.name ?? `#${id}`)

  return (
    <Table>
      <TableHeader>
        <TableRow>
          <TableHead>{t('Code')}</TableHead>
          <TableHead>{t('Role')}</TableHead>
          <TableHead>{t('Department')}</TableHead>
          <TableHead>{t('Status')}</TableHead>
          <TableHead>{t('Created')}</TableHead>
          <TableHead>{t('Expires')}</TableHead>
          <TableHead className='w-32' />
        </TableRow>
      </TableHeader>
      <TableBody>
        {props.invites.map((inv) => {
          const link = props.links[inv.id]
          return (
            <TableRow key={inv.id}>
              <TableCell className='font-mono text-xs'>
                {inv.codePrefix}...
              </TableCell>
              <TableCell>{roleLabel(inv.memberRole)}</TableCell>
              <TableCell>{projectName(inv.projectId)}</TableCell>
              <TableCell>
                <StatusBadge
                  label={stateLabel[inv.state]}
                  variant={STATE_VARIANT[inv.state]}
                  copyable={false}
                />
              </TableCell>
              <TableCell>
                {inv.createdAt > 0 ? formatDate(inv.createdAt) : '--'}
              </TableCell>
              <TableCell>
                {inv.expiresAt > 0 ? formatDate(inv.expiresAt) : t('Never')}
              </TableCell>
              <TableCell>
                <div className='flex items-center justify-end gap-1'>
                  {link != null && inv.state === 'active' && (
                    <CopyButton
                      value={link}
                      aria-label={t('Copy invitation link')}
                    />
                  )}
                  {inv.state === 'active' && (
                    <Button
                      variant='ghost'
                      size='sm'
                      onClick={() => props.onRevoke(inv)}
                    >
                      {t('Revoke')}
                    </Button>
                  )}
                </div>
              </TableCell>
            </TableRow>
          )
        })}
      </TableBody>
    </Table>
  )
}
