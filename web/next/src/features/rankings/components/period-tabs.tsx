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
import { useRef, type KeyboardEvent } from 'react';
import { useTranslation } from 'react-i18next';

import { cn } from '@/lib/utils';

import { PERIODS, type RankingPeriod } from '../lib/periods';

/** Underline tabs for the reporting window; arrow keys move between them. */
export function PeriodTabs(props: {
  period: RankingPeriod;
  onChange: (p: RankingPeriod) => void;
}) {
  const { t } = useTranslation();
  const refs = useRef<Array<HTMLButtonElement | null>>([]);

  const onKeyDown = (ev: KeyboardEvent, index: number) => {
    let next = index;
    if (ev.key === 'ArrowRight') next = (index + 1) % PERIODS.length;
    else if (ev.key === 'ArrowLeft') {
      next = (index - 1 + PERIODS.length) % PERIODS.length;
    } else return;
    ev.preventDefault();
    props.onChange(PERIODS[next].id);
    refs.current[next]?.focus();
  };

  return (
    <div
      role='tablist'
      aria-label={t('Period')}
      className='border-border/60 flex items-center border-b'
    >
      {PERIODS.map((p, i) => {
        const active = props.period === p.id;
        return (
          <button
            key={p.id}
            ref={(el) => {
              refs.current[i] = el;
            }}
            role='tab'
            type='button'
            aria-selected={active}
            tabIndex={active ? 0 : -1}
            data-testid={`rankings-period-${p.id}`}
            onClick={() => props.onChange(p.id)}
            onKeyDown={(ev) => onKeyDown(ev, i)}
            className={cn(
              'focus-visible:ring-ring/40 relative -mb-px rounded-sm px-3 py-2 text-sm font-medium transition-colors focus-visible:ring-2 focus-visible:outline-none',
              active
                ? 'text-foreground'
                : 'text-muted-foreground hover:text-foreground',
            )}
          >
            {t(p.label)}
            <span
              aria-hidden
              className={cn(
                'bg-foreground absolute inset-x-3 -bottom-px h-[2px] rounded-full transition-opacity',
                active ? 'opacity-100' : 'opacity-0',
              )}
            />
          </button>
        );
      })}
    </div>
  );
}
