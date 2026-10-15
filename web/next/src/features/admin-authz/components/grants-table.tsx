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
import { ShieldOff } from 'lucide-react'
import { useTranslation } from 'react-i18next'

import { formatTimestampToDate } from '@/components/format'
import { StatusBadge } from '@/components/status-badge'
import { Button } from '@/components/ui/button'
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from '@/components/ui/card'
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table'

import { grantStatus } from '../lib/grants'
import type { Grant, GrantStatus } from '../types'

const STATUS_BADGE: Record<
  GrantStatus,
  { label: string; variant: 'success' | 'warning' | 'neutral' }
> = {
  active: { label: 'Active', variant: 'success' },
  expired: { label: 'Expired', variant: 'warning' },
  revoked: { label: 'Revoked', variant: 'neutral' },
}

export function GrantsTable(props: {
  grants: Grant[]
  onRevoke: (grant: Grant) => void
}) {
  const { t } = useTranslation()

  return (
    <Card>
      <CardHeader>
        <CardTitle>{t('Issued grants')}</CardTitle>
        <CardDescription>
          {t('Every delegated grant: active, expired and withdrawn.')}
        </CardDescription>
      </CardHeader>
      <CardContent>
        {props.grants.length === 0 ? (
          <div
            className='text-muted-foreground flex flex-col items-center gap-2 py-10 text-sm'
            data-testid='authz-empty'
          >
            <ShieldOff className='size-6' />
            {t('No grants issued yet.')}
          </div>
        ) : (
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>{t('User ID')}</TableHead>
                <TableHead>{t('Resource')}</TableHead>
                <TableHead>{t('Action')}</TableHead>
                <TableHead>{t('Granted at')}</TableHead>
                <TableHead>{t('Expires at')}</TableHead>
                <TableHead>{t('Status')}</TableHead>
                <TableHead className='text-end'>
                  <span className='sr-only'>{t('Actions')}</span>
                </TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {props.grants.map((g) => {
                const status = grantStatus(g)
                const badge = STATUS_BADGE[status]
                return (
                  <TableRow key={g.id} data-testid={`authz-row-${g.id}`}>
                    <TableCell className='font-mono'>{g.user_id}</TableCell>
                    <TableCell className='text-muted-foreground font-mono'>
                      {g.resource}
                    </TableCell>
                    <TableCell className='text-muted-foreground font-mono'>
                      {g.action}
                    </TableCell>
                    <TableCell className='text-muted-foreground font-mono'>
                      {formatTimestampToDate(g.created_at)}
                    </TableCell>
                    <TableCell
                      className='text-muted-foreground font-mono'
                      data-testid={`authz-expires-${g.id}`}
                    >
                      {g.expires_at
                        ? formatTimestampToDate(g.expires_at)
                        : t('Never')}
                    </TableCell>
                    <TableCell data-testid={`authz-status-${g.id}`}>
                      <StatusBadge
                        label={t(badge.label)}
                        variant={badge.variant}
                        copyable={false}
                      />
                    </TableCell>
                    <TableCell className='text-end'>
                      {/* An expired-but-unrevoked row is still revocable:
                          revoked_at is genuinely null server-side. */}
                      {status !== 'revoked' && (
                        <Button
                          variant='ghost'
                          size='sm'
                          onClick={() => props.onRevoke(g)}
                          data-testid={`authz-revoke-${g.id}`}
                          aria-label={`${t('Revoke')} #${g.id}`}
                        >
                          {t('Revoke')}
                        </Button>
                      )}
                    </TableCell>
                  </TableRow>
                )
              })}
            </TableBody>
          </Table>
        )}
      </CardContent>
    </Card>
  )
}
