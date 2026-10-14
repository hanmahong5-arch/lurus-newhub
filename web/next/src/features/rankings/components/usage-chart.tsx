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
import { useMemo } from 'react';
import { useTranslation } from 'react-i18next';
import { Bar, BarChart, CartesianGrid, Tooltip, XAxis, YAxis } from 'recharts';

import { ChartContainer, type ChartConfig } from '@/components/ui/chart';

import { formatSharePct, formatTokens } from '../lib/format';
import {
  colorAt,
  toShareRows,
  type BucketedSeries,
  type SeriesKey,
} from '../lib/series';

export type ChartMode = 'tokens' | 'share';

interface TooltipEntry {
  dataKey?: string | number;
  value?: number | string;
}

interface TooltipProps {
  active?: boolean;
  label?: string | number;
  payload?: ReadonlyArray<TooltipEntry>;
  keys: SeriesKey[];
  mode: ChartMode;
  /** Raw (token) rows by label, so the share view can still show volume. */
  tokensByLabel: Map<string, Record<string, number | string>>;
  maxRows: number;
  totalLabel: string;
  moreLabel: (n: number) => string;
}

function UsageTooltip(props: TooltipProps) {
  if (!props.active || !props.payload?.length) return null;
  const raw = props.tokensByLabel.get(String(props.label)) ?? {};
  const entries = props.keys
    .map((k, i) => {
      const point = props.payload?.find((p) => p.dataKey === k.key);
      return {
        k,
        color: colorAt(i, k.other),
        value: Number(point?.value ?? 0),
        tokens: Number(raw[k.key] ?? 0),
      };
    })
    .filter((e) => e.value > 0)
    .sort((a, b) => b.value - a.value);
  if (entries.length === 0) return null;
  const visible = entries.slice(0, props.maxRows);
  const rest = entries.slice(props.maxRows);
  const total = entries.reduce((s, e) => s + e.tokens, 0);
  return (
    <div className='border-border/50 bg-background grid min-w-40 gap-1.5 rounded-lg border px-2.5 py-1.5 text-xs shadow-xl'>
      <div className='font-medium'>{String(props.label)}</div>
      <div className='text-muted-foreground flex justify-between gap-4'>
        <span>{props.totalLabel}</span>
        <span className='font-mono'>{formatTokens(total)}</span>
      </div>
      {visible.map((e) => (
        <div key={e.k.key} className='flex items-center gap-2'>
          <span
            aria-hidden='true'
            className='size-2.5 shrink-0 rounded-sm'
            style={{ background: e.color }}
          />
          <span className='min-w-0 flex-1 truncate'>{e.k.name}</span>
          <span className='font-mono tabular-nums'>
            {props.mode === 'share'
              ? `${formatSharePct(e.value * 100)} · ${formatTokens(e.tokens)}`
              : formatTokens(e.value)}
          </span>
        </div>
      ))}
      {rest.length > 0 && (
        <div className='text-muted-foreground'>
          {props.moreLabel(rest.length)}
        </div>
      )}
    </div>
  );
}

/**
 * Stacked bars of the leaderboard's top names over the window: absolute
 * tokens, or each bar normalised to 100% for a share view.
 */
export function UsageChart(props: {
  data: BucketedSeries;
  mode: ChartMode;
  ariaLabel: string;
}) {
  const { t } = useTranslation();
  const { keys, rows } = props.data;
  const shown = useMemo(
    () => (props.mode === 'share' ? toShareRows(rows, keys) : rows),
    [props.mode, rows, keys],
  );
  const tokensByLabel = useMemo(
    () => new Map(rows.map((r) => [r.label, r])),
    [rows],
  );
  const config = useMemo<ChartConfig>(
    () =>
      Object.fromEntries(
        keys.map((k, i) => [
          k.key,
          { label: k.name, color: colorAt(i, k.other) },
        ]),
      ),
    [keys],
  );

  if (rows.length === 0 || keys.length === 0) {
    return (
      <div
        className='text-muted-foreground flex h-60 items-center justify-center text-xs sm:h-72'
        data-testid='rankings-chart-empty'
      >
        {t('No history data available')}
      </div>
    );
  }

  return (
    <div data-testid='rankings-chart'>
      <ChartContainer
        config={config}
        className='aspect-auto h-60 w-full sm:h-72'
        role='img'
        aria-label={props.ariaLabel}
      >
        <BarChart data={shown} margin={{ top: 4, right: 4, left: 0 }}>
          <CartesianGrid vertical={false} strokeDasharray='3 3' />
          <XAxis
            dataKey='label'
            tickLine={false}
            axisLine={false}
            minTickGap={24}
            fontSize={10}
          />
          <YAxis
            tickLine={false}
            axisLine={false}
            width={44}
            fontSize={10}
            domain={props.mode === 'share' ? [0, 1] : undefined}
            tickFormatter={(v: number) =>
              props.mode === 'share'
                ? `${Math.round(v * 100)}%`
                : formatTokens(v)
            }
          />
          <Tooltip
            cursor={{ fillOpacity: 0.08 }}
            content={
              <UsageTooltip
                keys={keys}
                mode={props.mode}
                tokensByLabel={tokensByLabel}
                maxRows={10}
                totalLabel={t('Total:')}
                moreLabel={(n) => t('+{{count}} more', { count: n })}
              />
            }
          />
          {keys.map((k, i) => (
            <Bar
              key={k.key}
              dataKey={k.key}
              stackId='usage'
              fill={colorAt(i, k.other)}
              isAnimationActive={false}
            />
          ))}
        </BarChart>
      </ChartContainer>
      <ul
        className='mt-3 flex flex-wrap gap-x-4 gap-y-1.5 text-xs'
        aria-label={t('Series')}
      >
        {keys.map((k, i) => (
          <li key={k.key} className='flex min-w-0 items-center gap-1.5'>
            <span
              aria-hidden='true'
              className='size-2.5 shrink-0 rounded-sm'
              style={{ background: colorAt(i, k.other) }}
            />
            <span className='max-w-40 truncate' title={k.name}>
              {k.name}
            </span>
            <span className='text-muted-foreground font-mono tabular-nums'>
              {formatTokens(k.tokens)}
            </span>
          </li>
        ))}
      </ul>
    </div>
  );
}
