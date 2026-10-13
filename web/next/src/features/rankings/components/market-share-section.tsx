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
import { PieChart } from 'lucide-react';
import { useMemo, useState } from 'react';
import { useTranslation } from 'react-i18next';

import { ErrorState } from '@/components/error-state';
import { Skeleton } from '@/components/ui/skeleton';
import { Tabs, TabsList, TabsTrigger } from '@/components/ui/tabs';
import { currentLocale } from '@/lib/locale';

import { rankingsQueryOptions } from '../lib/api';
import {
  hoursOf,
  periodCaption,
  SHARE_DIMENSIONS,
  type RankingPeriod,
} from '../lib/periods';
import { bucketSeries } from '../lib/series';
import { Leaderboard } from './leaderboard';
import { UsageChart } from './usage-chart';

type ShareDimension = (typeof SHARE_DIMENSIONS)[number]['id'];

/**
 * Who the traffic belongs to: share of tokens by group, product, member, key,
 * endpoint kind or billing unit. Vendor is deliberately not offered; it is a
 * platform-staff view and would reveal the supply chain.
 */
export function MarketShareSection(props: { period: RankingPeriod }) {
  const { t, i18n } = useTranslation();
  const [dim, setDim] = useState<ShareDimension>('group');
  const hours = hoursOf(props.period);
  const q = useQuery(rankingsQueryOptions(dim, hours));
  const data = q.data;
  const label = t(SHARE_DIMENSIONS.find((d) => d.id === dim)?.label ?? dim);

  const chart = useMemo(
    () =>
      data
        ? bucketSeries(data.series, {
            hours: data.hours,
            window: data.window,
            otherLabel: t('Other'),
            locale: currentLocale(),
          })
        : null,
    // The locale follows the UI language, which i18n.language tracks.
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [data, t, i18n.language],
  );

  let body;
  if (q.isPending) {
    body = <Skeleton className='mx-5 mb-5 h-72 w-auto' />;
  } else if (q.isError) {
    body = (
      <div data-testid='rankings-share-error'>
        <ErrorState
          title={t('Failed to load rankings')}
          description={q.error instanceof Error ? q.error.message : undefined}
          onRetry={() => void q.refetch()}
          className='min-h-40'
        />
      </div>
    );
  } else if (!data || data.rows.length === 0) {
    body = (
      <div className='text-muted-foreground/80 px-5 py-10 text-center text-sm'>
        {t('No traffic in this period')}
      </div>
    );
  } else {
    body = (
      <>
        <div className='px-5 pb-5'>
          {chart && (
            <UsageChart
              data={chart}
              mode='share'
              ariaLabel={t('Share of tokens over time')}
            />
          )}
        </div>
        <div className='border-t px-2 pt-2 pb-3'>
          <Leaderboard
            rows={data.rows}
            nameLabel={label}
            testId='rankings-share-table'
          />
        </div>
      </>
    );
  }

  return (
    <section
      className='bg-card overflow-hidden rounded-lg border'
      data-testid='rankings-share'
    >
      <header className='px-5 py-4'>
        <h2 className='text-foreground inline-flex items-center gap-2 text-base font-semibold'>
          <PieChart className='text-primary size-4' />
          {t('Market Share')}
        </h2>
        <p className='text-muted-foreground mt-1 text-sm'>
          {t('Token share by {{dimension}} across {{period}}', {
            dimension: label.toLowerCase(),
            period: t(periodCaption(props.period)),
          })}
        </p>
        <Tabs
          value={dim}
          onValueChange={(v) => setDim(v as ShareDimension)}
          className='mt-3'
        >
          <TabsList className='h-auto flex-wrap'>
            {SHARE_DIMENSIONS.map((d) => (
              <TabsTrigger
                key={d.id}
                value={d.id}
                className='text-xs'
                data-testid={`rankings-dim-${d.id}`}
              >
                {t(d.label)}
              </TabsTrigger>
            ))}
          </TabsList>
        </Tabs>
      </header>
      {body}
    </section>
  );
}
