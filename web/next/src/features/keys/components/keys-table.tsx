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
import { Code2, Pencil, RefreshCw, Trash2 } from 'lucide-react'
import { useTranslation } from 'react-i18next'

import { CopyButton } from '@/components/copy-button'
import { StatusBadge, type StatusVariant } from '@/components/status-badge'
import { Button } from '@/components/ui/button'
import { Checkbox } from '@/components/ui/checkbox'
import { Switch } from '@/components/ui/switch'
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table'
import type { MoneyConfig } from '@/lib/money'
import { formatDate } from '@/lib/time'

import {
  isStoredEnabled,
  tokenState,
  type TokenState,
} from '../lib/map'
import { quotaView } from '../lib/quota'
import type { ApiToken } from '../types'

const STATE_VARIANT: Record<TokenState, StatusVariant> = {
  enabled: 'success',
  'near-cap': 'warning',
  disabled: 'neutral',
  expired: 'danger',
  exhausted: 'danger',
}

interface KeysTableProps {
  tokens: ApiToken[]
  money: MoneyConfig | null
  nowSec: number
  /** Plaintext keys known in this session (create / rotate), by token id. */
  secrets: Record<number, string>
  selected: Set<number>
  busyId: number | null
  onToggleSelect: (id: number, on: boolean) => void
  onToggleAll: (on: boolean) => void
  onToggleStatus: (token: ApiToken, enable: boolean) => void
  onEdit: (token: ApiToken) => void
  onSnippets: (token: ApiToken) => void
  onRotate: (token: ApiToken) => void
  onDelete: (token: ApiToken) => void
}

export function KeysTable(props: KeysTableProps) {
  const { t } = useTranslation()
  const { tokens, selected } = props
  const allSelected = tokens.length > 0 && tokens.every((k) => selected.has(k.id))

  const stateLabel: Record<TokenState, string> = {
    enabled: t('Enabled'),
    'near-cap': t('Near quota limit'),
    disabled: t('Disabled'),
    expired: t('Expired'),
    exhausted: t('Quota exhausted'),
  }

  return (
    <Table>
      <TableHeader>
        <TableRow>
          <TableHead className='w-8'>
            <Checkbox
              checked={allSelected}
              onCheckedChange={(v) => props.onToggleAll(v === true)}
              aria-label={t('Select all on this page')}
            />
          </TableHead>
          <TableHead>{t('Name')}</TableHead>
          <TableHead>{t('Key')}</TableHead>
          <TableHead>{t('Status')}</TableHead>
          <TableHead>{t('Quota')}</TableHead>
          <TableHead>{t('Expires')}</TableHead>
          <TableHead>{t('Last used')}</TableHead>
          <TableHead className='text-end'>{t('Actions')}</TableHead>
        </TableRow>
      </TableHeader>
      <TableBody>
        {tokens.map((token) => {
          const state = tokenState(token, props.nowSec)
          const q = quotaView(token, props.money)
          const secret = props.secrets[token.id]
          const busy = props.busyId === token.id
          return (
            <TableRow key={token.id} data-testid={`key-row-${token.id}`}>
              <TableCell>
                <Checkbox
                  checked={selected.has(token.id)}
                  onCheckedChange={(v) =>
                    props.onToggleSelect(token.id, v === true)
                  }
                  aria-label={t('Select {{name}}', { name: token.name })}
                />
              </TableCell>
              <TableCell className='max-w-48 truncate font-medium'>
                {token.name}
              </TableCell>
              <TableCell>
                <div className='flex items-center gap-1'>
                  <code className='font-mono text-xs'>
                    {secret ?? token.key}
                  </code>
                  {secret != null && (
                    <CopyButton
                      value={secret}
                      size='icon'
                      aria-label={t('Copy key')}
                    />
                  )}
                </div>
              </TableCell>
              <TableCell>
                <StatusBadge
                  label={stateLabel[state]}
                  variant={STATE_VARIANT[state]}
                  copyable={false}
                />
              </TableCell>
              <TableCell>
                {q.unlimited ? (
                  <div className='text-sm'>
                    <span>{t('Unlimited')}</span>
                    <span className='text-muted-foreground ms-2 text-xs'>
                      {t('used {{amount}}', { amount: q.used })}
                    </span>
                  </div>
                ) : (
                  <div className='grid min-w-32 gap-1'>
                    <span className='text-sm'>
                      {q.used} / {q.total}
                    </span>
                    {q.usedRatio != null && (
                      <div
                        className='bg-muted h-1 w-full overflow-hidden rounded-full'
                        role='progressbar'
                        aria-valuemin={0}
                        aria-valuemax={100}
                        aria-valuenow={Math.round(q.usedRatio * 100)}
                      >
                        <div
                          className={
                            q.usedRatio >= 0.9 ? 'bg-destructive h-1' : 'bg-primary h-1'
                          }
                          style={{ width: `${q.usedRatio * 100}%` }}
                        />
                      </div>
                    )}
                  </div>
                )}
              </TableCell>
              <TableCell className='text-sm'>
                {token.expired_time > 0
                  ? formatDate(token.expired_time)
                  : t('Never')}
              </TableCell>
              <TableCell className='text-sm'>
                {token.accessed_time > 0 ? formatDate(token.accessed_time) : '-'}
              </TableCell>
              <TableCell>
                <div className='flex items-center justify-end gap-1'>
                  <Switch
                    size='sm'
                    checked={isStoredEnabled(token)}
                    disabled={busy}
                    onCheckedChange={(v) => props.onToggleStatus(token, v)}
                    aria-label={
                      isStoredEnabled(token)
                        ? t('Disable {{name}}', { name: token.name })
                        : t('Enable {{name}}', { name: token.name })
                    }
                  />
                  <Button
                    variant='ghost'
                    size='icon-sm'
                    onClick={() => props.onSnippets(token)}
                    aria-label={t('Usage examples for {{name}}', {
                      name: token.name,
                    })}
                  >
                    <Code2 />
                  </Button>
                  <Button
                    variant='ghost'
                    size='icon-sm'
                    onClick={() => props.onEdit(token)}
                    aria-label={t('Edit {{name}}', { name: token.name })}
                  >
                    <Pencil />
                  </Button>
                  <Button
                    variant='ghost'
                    size='icon-sm'
                    onClick={() => props.onRotate(token)}
                    aria-label={t('Rotate {{name}}', { name: token.name })}
                  >
                    <RefreshCw />
                  </Button>
                  <Button
                    variant='ghost'
                    size='icon-sm'
                    onClick={() => props.onDelete(token)}
                    aria-label={t('Delete {{name}}', { name: token.name })}
                  >
                    <Trash2 />
                  </Button>
                </div>
              </TableCell>
            </TableRow>
          )
        })}
      </TableBody>
    </Table>
  )
}

