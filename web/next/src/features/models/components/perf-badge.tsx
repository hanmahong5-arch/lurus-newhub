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
import type { ReactNode } from 'react';
import { useTranslation } from 'react-i18next';

import { cn } from '@/lib/utils';

import type { ModelEntry } from '../lib/catalog';
import {
  formatLatency,
  formatSuccessRate,
  successRateDotClass,
  successRateOf,
  successRateTextClass,
} from '../lib/perf';
import {
  perfWindowLabelKey,
  useNoMeasurement,
  usePerfWindow,
} from './perf-window';

/** Success rate with a status dot; an unmeasured model shows a dash. */
export function SuccessRate(props: { entry: ModelEntry; className?: string }) {
  const rate = successRateOf(props.entry);
  return (
    <span
      className={cn(
        'inline-flex items-center gap-1.5 font-mono tabular-nums',
        successRateTextClass(rate),
        props.className,
      )}
    >
      <span
        aria-hidden='true'
        className={cn('size-2 rounded-full', successRateDotClass(rate))}
      />
      {formatSuccessRate(rate)}
    </span>
  );
}

/**
 * Card footer strip: success rate, p50 and p95 over the chosen window. There
 * is no first-token latency or throughput behind the data, so neither is
 * drawn. Models without enough traffic say so instead of showing a number.
 */
export function PerfBadge(props: {
  entry: ModelEntry;
  className?: string;
  children?: ReactNode;
}) {
  const { t } = useTranslation();
  const e = props.entry;
  const windowHours = usePerfWindow();
  const reason = useNoMeasurement(e);
  const id = `model-perf-${e.id}`;
  return (
    <div
      aria-label={t('Performance over the {{window}}', {
        window: t(perfWindowLabelKey(windowHours)),
      })}
      className={cn(
        'flex w-full min-w-0 items-center justify-between gap-3',
        props.className,
      )}
    >
      {reason ? (
        <span className='text-muted-foreground text-xs' data-testid={id}>
          {reason}
        </span>
      ) : (
        <dl
          className='flex min-w-0 items-start gap-5 text-xs tabular-nums'
          data-testid={id}
        >
          <div className='shrink-0'>
            <dt
              className='text-muted-foreground text-[11px] leading-4'
              title={t('Share of requests that did not fail')}
            >
              {t('Success')}
            </dt>
            <dd className='mt-1'>
              <SuccessRate entry={e} />
            </dd>
          </div>
          <div className='shrink-0'>
            <dt className='text-muted-foreground text-[11px] leading-4'>p50</dt>
            <dd className='mt-1 font-mono whitespace-nowrap'>
              {formatLatency(e.p50Ms)}
            </dd>
          </div>
          <div className='shrink-0'>
            <dt className='text-muted-foreground text-[11px] leading-4'>p95</dt>
            <dd className='mt-1 font-mono whitespace-nowrap'>
              {formatLatency(e.p95Ms)}
            </dd>
          </div>
        </dl>
      )}
      {props.children}
    </div>
  );
}
