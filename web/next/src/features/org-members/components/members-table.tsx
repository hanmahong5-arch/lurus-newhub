import { useTranslation } from 'react-i18next'

import { StatusBadge } from '@/components/status-badge'
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

import { roleVariant, useRoleLabel } from '../lib/labels'
import type { Member } from '../types'

interface MembersTableProps {
  members: Member[]
  selfId: number
  canEdit: boolean
  onEditRole: (member: Member) => void
}

export function MembersTable(props: MembersTableProps) {
  const { t } = useTranslation()
  const roleLabel = useRoleLabel()
  const payerText = (v: boolean | null) => {
    if (v === null) return '--'
    return v ? t('Yes') : t('No')
  }

  return (
    <Table>
      <TableHeader>
        <TableRow>
          <TableHead>{t('Name')}</TableHead>
          <TableHead>{t('Email')}</TableHead>
          <TableHead>{t('Role')}</TableHead>
          <TableHead>{t('Payer')}</TableHead>
          <TableHead>{t('Departments')}</TableHead>
          <TableHead>{t('Joined')}</TableHead>
          {props.canEdit && <TableHead className='w-24' />}
        </TableRow>
      </TableHeader>
      <TableBody>
        {props.members.map((m) => (
          <TableRow key={m.userId}>
            <TableCell className='font-medium'>
              {m.name}
              {m.userId === props.selfId && (
                <span className='text-muted-foreground ml-2 text-xs'>
                  {t('(you)')}
                </span>
              )}
            </TableCell>
            <TableCell>{m.email || '--'}</TableCell>
            <TableCell>
              <StatusBadge
                label={roleLabel(m.role)}
                variant={roleVariant(m.role)}
                copyable={false}
              />
            </TableCell>
            <TableCell>
              {payerText(m.isPayer)}
            </TableCell>
            <TableCell>
              {m.departments.length > 0 ? m.departments.join(', ') : '--'}
            </TableCell>
            <TableCell>{m.joinedAt > 0 ? formatDate(m.joinedAt) : '--'}</TableCell>
            {props.canEdit && (
              <TableCell>
                <Button
                  variant='ghost'
                  size='sm'
                  onClick={() => props.onEditRole(m)}
                >
                  {t('Change role')}
                </Button>
              </TableCell>
            )}
          </TableRow>
        ))}
      </TableBody>
    </Table>
  )
}
