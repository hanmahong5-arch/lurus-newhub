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
import { useQuery } from '@tanstack/react-query';
import { useState } from 'react';
import { useTranslation } from 'react-i18next';

import { ErrorState } from '@/components/error-state';
import { PageHeader } from '@/components/layout/page-header';
import { Skeleton } from '@/components/ui/skeleton';
import { currentLocale } from '@/lib/locale';

import { MarketShareSection } from './components/market-share-section';
import { ModelsSection } from './components/models-section';
import { PeriodTabs } from './components/period-tabs';
import { PulseSection } from './components/pulse-section';
import { rankingsQueryOptions } from './lib/api';
import { DEFAULT_PERIOD, hoursOf, type RankingPeriod } from './lib/periods';

function RankingsLoading() {
  return (
    <div className='space-y-6' aria-busy='true' data-testid='rankings-loading'>
      <Skeleton className='h-[420px] w-full rounded-xl' />
      <Skeleton className='h-[180px] w-full rounded-xl' />
    </div>
  );
}

/**
 * Model leaderboard for the caller's own tenant (tenant admins). Volumes are
 * never cross-tenant; the longest window is 30 days.
 */
export function RankingsPage() {
  const { t } = useTranslation();
  const [period, setPeriod] = useState<RankingPeriod>(DEFAULT_PERIOD);
  const q = useQuery(rankingsQueryOptions('model', hoursOf(period)));
  const data = q.data;

  let body;
  if (q.isPending) {
    body = <RankingsLoading />;
  } else if (q.isError) {
    body = (
      <div data-testid='rankings-error'>
        <ErrorState
          title={t('Failed to load rankings')}
          description={q.error instanceof Error ? q.error.message : undefined}
          onRetry={() => void q.refetch()}
        />
      </div>
    );
  } else {
    body = (
      <div className='space-y-6'>
        <ModelsSection data={q.data} period={period} />
        <MarketShareSection period={period} />
        <PulseSection rows={q.data.rows} />
      </div>
    );
  }

  const updated =
    data && data.cached_at > 0
      ? t('Updated {{time}}', {
          time: new Date(data.cached_at * 1000).toLocaleTimeString(
            currentLocale(),
            { hour: '2-digit', minute: '2-digit' },
          ),
        })
      : undefined;

  return (
    <>
      <PageHeader
        title={t('Rankings')}
        description={t(
          'The most-used models and who uses them, from your own tenant traffic.',
        )}
      />
      <div className='mt-5 flex flex-wrap items-end justify-between gap-3'>
        <PeriodTabs period={period} onChange={setPeriod} />
        {updated && (
          <span
            className='text-muted-foreground text-xs'
            data-testid='rankings-updated'
            title={t('Rankings refresh at most every 5 minutes')}
          >
            {updated}
          </span>
        )}
      </div>
      <div className='mt-6'>{body}</div>
    </>
  );
}
