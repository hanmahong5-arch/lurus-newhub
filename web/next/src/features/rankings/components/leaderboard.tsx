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
import { ArrowDownRight, ArrowUpRight, Minus } from 'lucide-react';
import { useTranslation } from 'react-i18next';

import { Badge } from '@/components/ui/badge';
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table';
import { useQuota } from '@/hooks/use-quota';
import { cn } from '@/lib/utils';

import type { RankingRow } from '../lib/api';
import { formatSharePct, formatTokens } from '../lib/format';

/** Request growth against the previous window; null = nothing to compare. */
export function GrowthText(props: {
  value: number | null | undefined;
  className?: string;
}) {
  const { t } = useTranslation();
  const v = props.value;
  if (v == null || !Number.isFinite(v)) {
    return (
      <span
        className={cn('text-muted-foreground font-mono', props.className)}
        title={t('No previous period to compare with')}
      >
        —
      </span>
    );
  }
  if (v === 0) {
    return (
      <span
        className={cn(
          'text-muted-foreground/80 font-mono tabular-nums',
          props.className,
        )}
      >
        0%
      </span>
    );
  }
  const up = v > 0;
  return (
    <span
      className={cn(
        'font-mono tabular-nums',
        up
          ? 'text-emerald-600 dark:text-emerald-400'
          : 'text-rose-600 dark:text-rose-400',
        props.className,
      )}
    >
      {up ? '↑' : '↓'}
      {Math.abs(v).toFixed(Math.abs(v) >= 100 ? 0 : 1)}%
    </span>
  );
}

/** Rank movement against the previous window: arrow + places, NEW, or flat. */
export function RankChange(props: { row: RankingRow }) {
  const { t } = useTranslation();
  const { row } = props;
  if (row.is_new) {
    return (
      <Badge variant='secondary' data-testid={`rank-new-${row.name}`}>
        {t('New')}
      </Badge>
    );
  }
  if (row.rank_delta === 0) {
    return (
      <span
        className='text-muted-foreground inline-flex'
        title={t('No change in rank')}
      >
        <Minus aria-hidden className='size-3.5' />
        <span className='sr-only'>{t('No change in rank')}</span>
      </span>
    );
  }
  const up = row.rank_delta > 0;
  const Icon = up ? ArrowUpRight : ArrowDownRight;
  return (
    <span
      data-testid={`rank-delta-${row.name}`}
      className={cn(
        'inline-flex items-center gap-0.5 font-mono text-xs font-semibold tabular-nums',
        up
          ? 'text-emerald-600 dark:text-emerald-400'
          : 'text-rose-600 dark:text-rose-400',
      )}
    >
      <Icon aria-hidden className='size-3' />
      {Math.abs(row.rank_delta)}
      <span className='sr-only'>
        {up ? t('Moved up') : t('Moved down')}
      </span>
    </span>
  );
}

/**
 * Ranked table: position and movement, requests, tokens, share of the whole
 * window (not only of the rows shown), spend and growth.
 */
export function Leaderboard(props: {
  rows: RankingRow[];
  /** Header of the name column, e.g. "Model". */
  nameLabel: string;
  testId?: string;
}) {
  const { t } = useTranslation();
  const { formatQuota } = useQuota();
  return (
    <div className='overflow-x-auto' data-testid={props.testId}>
      <Table>
        <TableHeader>
          <TableRow>
            <TableHead className='w-10 text-right'>#</TableHead>
            <TableHead className='w-16'>{t('Change')}</TableHead>
            <TableHead>{props.nameLabel}</TableHead>
            <TableHead className='text-right'>{t('Requests')}</TableHead>
            <TableHead className='text-right'>{t('Tokens')}</TableHead>
            <TableHead className='min-w-32'>{t('Token share')}</TableHead>
            <TableHead className='text-right'>{t('Spend')}</TableHead>
            <TableHead className='text-right'>{t('Growth')}</TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          {props.rows.map((r) => (
            <TableRow key={r.name} data-testid={`rank-row-${r.name}`}>
              <TableCell className='text-muted-foreground text-right font-mono text-xs tabular-nums'>
                {r.rank}
              </TableCell>
              <TableCell>
                <RankChange row={r} />
              </TableCell>
              <TableCell className='max-w-64 truncate font-mono text-sm font-medium'>
                <span title={r.name}>{r.name}</span>
              </TableCell>
              <TableCell className='text-right font-mono tabular-nums'>
                {r.requests.toLocaleString()}
              </TableCell>
              <TableCell className='text-right font-mono font-semibold tabular-nums'>
                {formatTokens(r.total_tokens)}
              </TableCell>
              <TableCell>
                <div className='flex items-center gap-2'>
                  <div
                    className='bg-muted h-1.5 w-16 flex-1 rounded'
                    aria-hidden='true'
                  >
                    <div
                      className='bg-primary h-1.5 rounded'
                      style={{
                        width: `${Math.min(100, Math.max(0, r.token_share_pct))}%`,
                      }}
                    />
                  </div>
                  <span className='w-12 text-right font-mono text-xs tabular-nums'>
                    {formatSharePct(r.token_share_pct)}
                  </span>
                </div>
              </TableCell>
              <TableCell className='text-right font-mono tabular-nums'>
                {formatQuota(r.quota)}
              </TableCell>
              <TableCell className='text-right'>
                <GrowthText value={r.requests_growth_pct} />
              </TableCell>
            </TableRow>
          ))}
        </TableBody>
      </Table>
    </div>
  );
}
