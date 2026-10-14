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
import { ArrowDownRight, ArrowUpRight, Sparkles, TrendingDown, TrendingUp } from 'lucide-react';
import type { ReactNode } from 'react';
import { useTranslation } from 'react-i18next';

import { cn } from '@/lib/utils';

import type { RankingRow } from '../lib/api';
import { formatTokens, movers } from '../lib/format';

function PulseCard(props: {
  title: string;
  description: string;
  icon: ReactNode;
  testId: string;
  children: ReactNode;
}) {
  return (
    <div
      className='bg-card overflow-hidden rounded-lg border'
      data-testid={props.testId}
    >
      <header className='border-b px-4 py-3'>
        <h3 className='text-foreground inline-flex items-center gap-2 text-sm font-semibold'>
          {props.icon}
          {props.title}
        </h3>
        <p className='text-muted-foreground/80 mt-0.5 text-xs'>
          {props.description}
        </p>
      </header>
      <div className='py-1'>{props.children}</div>
    </div>
  );
}

function PulseRow(props: {
  row: RankingRow;
  intent: 'up' | 'down' | 'new';
}) {
  const { t } = useTranslation();
  const { row, intent } = props;
  return (
    <li className='flex items-center gap-3 px-4 py-2'>
      <div className='min-w-0 flex-1'>
        <span
          className='text-foreground block truncate font-mono text-xs font-medium'
          title={row.name}
        >
          {row.name}
        </span>
        <p className='text-muted-foreground/80 truncate text-[11px]'>
          #{row.rank} · {formatTokens(row.total_tokens)} {t('tokens')}
        </p>
      </div>
      {intent === 'new' ? (
        <span className='text-xs font-semibold'>{t('New')}</span>
      ) : (
        <span
          className={cn(
            'inline-flex shrink-0 items-center gap-0.5 font-mono text-xs font-semibold tabular-nums',
            intent === 'up'
              ? 'text-emerald-600 dark:text-emerald-400'
              : 'text-rose-600 dark:text-rose-400',
          )}
        >
          {intent === 'up' ? (
            <ArrowUpRight className='size-3' />
          ) : (
            <ArrowDownRight className='size-3' />
          )}
          {Math.abs(row.rank_delta)}
        </span>
      )}
    </li>
  );
}

function Empty(props: { label: string }) {
  return (
    <div className='text-muted-foreground/80 px-4 py-6 text-center text-xs'>
      {props.label}
    </div>
  );
}

/** Rank movement against the previous period, from the model leaderboard. */
export function PulseSection(props: { rows: RankingRow[] }) {
  const { t } = useTranslation();
  const { up, down, fresh } = movers(props.rows);
  return (
    <section
      className='grid grid-cols-1 gap-4 lg:grid-cols-3'
      data-testid='rankings-pulse'
    >
      <PulseCard
        testId='rankings-up'
        title={t('Trending up')}
        description={t('Models climbing the leaderboard')}
        icon={<TrendingUp className='size-4 text-emerald-500' />}
      >
        {up.length === 0 ? (
          <Empty label={t('No notable climbers right now')} />
        ) : (
          <ul>
            {up.map((r) => (
              <PulseRow key={r.name} row={r} intent='up' />
            ))}
          </ul>
        )}
      </PulseCard>
      <PulseCard
        testId='rankings-down'
        title={t('Trending down')}
        description={t('Models losing positions')}
        icon={<TrendingDown className='size-4 text-rose-500' />}
      >
        {down.length === 0 ? (
          <Empty label={t('No notable drops right now')} />
        ) : (
          <ul>
            {down.map((r) => (
              <PulseRow key={r.name} row={r} intent='down' />
            ))}
          </ul>
        )}
      </PulseCard>
      <PulseCard
        testId='rankings-new'
        title={t('New this period')}
        description={t('Models with no traffic in the previous period')}
        icon={<Sparkles className='size-4 text-amber-500' />}
      >
        {fresh.length === 0 ? (
          <Empty label={t('No new models right now')} />
        ) : (
          <ul>
            {fresh.map((r) => (
              <PulseRow key={r.name} row={r} intent='new' />
            ))}
          </ul>
        )}
      </PulseCard>
    </section>
  );
}
