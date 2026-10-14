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
import { BarChart3, Trophy } from 'lucide-react';
import { useMemo } from 'react';
import { useTranslation } from 'react-i18next';

import { currentLocale } from '@/lib/locale';

import type { RankingsData } from '../lib/api';
import { formatTokens } from '../lib/format';
import { periodCaption, type RankingPeriod } from '../lib/periods';
import { bucketSeries } from '../lib/series';
import { Leaderboard } from './leaderboard';
import { UsageChart } from './usage-chart';

/** Top models: stacked usage over time, then the ranked table. */
export function ModelsSection(props: {
  data: RankingsData;
  period: RankingPeriod;
}) {
  const { t, i18n } = useTranslation();
  const { data } = props;
  const chart = useMemo(
    () =>
      bucketSeries(data.series, {
        hours: data.hours,
        window: data.window,
        otherLabel: t('Other'),
        locale: currentLocale(),
      }),
    // The locale follows the UI language, which i18n.language tracks.
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [data, t, i18n.language],
  );

  return (
    <section
      className='bg-card overflow-hidden rounded-lg border'
      data-testid='rankings-models'
    >
      <header className='flex items-start justify-between gap-4 px-5 py-4'>
        <div className='min-w-0 flex-1'>
          <h2 className='text-foreground inline-flex items-center gap-2 text-base font-semibold'>
            <BarChart3 className='text-primary size-4' />
            {t('Top Models')}
          </h2>
          <p className='text-muted-foreground mt-1 text-sm'>
            {t('Token usage by model across {{period}}', {
              period: t(periodCaption(props.period)),
            })}
          </p>
        </div>
        <div className='shrink-0 text-right'>
          <div
            className='text-foreground font-mono text-2xl font-semibold tabular-nums'
            data-testid='rankings-total-tokens'
          >
            {formatTokens(data.total_tokens)}
          </div>
          <div className='text-muted-foreground/80 text-[10px] font-medium tracking-widest uppercase'>
            {t('tokens')}
          </div>
        </div>
      </header>

      <div className='px-5 pb-5'>
        <UsageChart
          data={chart}
          mode='tokens'
          ariaLabel={t('Token usage by model over time')}
        />
      </div>

      <div className='border-t'>
        <header className='px-5 pt-4 pb-2'>
          <h3 className='text-foreground inline-flex items-center gap-2 text-sm font-semibold'>
            <Trophy className='size-3.5 text-amber-500' />
            {t('Model leaderboard')}
          </h3>
          <p className='text-muted-foreground/80 mt-0.5 text-xs'>
            {t('Ranked by tokens; change is against the previous period')}
          </p>
        </header>
        {data.rows.length === 0 ? (
          <div className='text-muted-foreground/80 px-5 py-8 text-center text-sm'>
            {t('No model traffic in this period')}
          </div>
        ) : (
          <div className='px-2 pb-3'>
            <Leaderboard
              rows={data.rows}
              nameLabel={t('Model')}
              testId='rankings-model-table'
            />
          </div>
        )}
      </div>
    </section>
  );
}
