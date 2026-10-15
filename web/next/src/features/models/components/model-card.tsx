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
import { ChevronRight } from 'lucide-react';
import { memo } from 'react';
import { useTranslation } from 'react-i18next';

import { CopyButton } from '@/components/copy-button';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import {
  Card,
  CardContent,
  CardFooter,
  CardHeader,
} from '@/components/ui/card';
import { cn } from '@/lib/utils';

import {
  capabilityLabelKey,
  formatCompact,
  type ModelEntry,
} from '../lib/catalog';
import { CompareToggle } from './compare';
import { PerfBadge } from './perf-badge';
import { BillingModeBadge, CallableBadge, PriceGrid } from './price';
import { VendorIcon } from './vendor-icon';

/** "a, b +2": the first `max` items, then a count of the rest. */
function ShortList(props: { label: string; items: string[]; max: number }) {
  const { items, max } = props;
  if (items.length === 0) return null;
  return (
    <div className='flex min-w-0 items-baseline gap-1.5'>
      <dt className='text-muted-foreground shrink-0'>{props.label}</dt>
      <dd className='flex min-w-0 items-baseline gap-1'>
        <span className='truncate' title={items.join(', ')}>
          {items.slice(0, max).join(', ')}
        </span>
        {items.length > max && (
          <span
            className='text-muted-foreground shrink-0'
            title={items.slice(max).join(', ')}
          >
            +{items.length - max}
          </span>
        )}
      </dd>
    </div>
  );
}

export const ModelCard = memo(function ModelCard(props: {
  entry: ModelEntry;
  onOpen: (id: string) => void;
}) {
  const { t } = useTranslation();
  const e = props.entry;
  return (
    <Card
      data-testid={`model-card-${e.id}`}
      onClick={() => props.onOpen(e.id)}
      className='hover:ring-foreground/20 h-full min-w-0 cursor-pointer gap-3 transition-colors'
    >
      <CardHeader className='flex flex-row items-start gap-3'>
        <div className='bg-muted/50 flex size-10 shrink-0 items-center justify-center rounded-lg'>
          <VendorIcon vendor={e.vendor} icon={e.icon} size={28} />
        </div>
        <div className='min-w-0 flex-1'>
          <h3
            className='line-clamp-2 font-mono text-[15px] leading-snug font-semibold [overflow-wrap:anywhere]'
            title={e.id}
          >
            {e.id}
          </h3>
          <p className='text-muted-foreground mt-1 truncate text-xs'>
            {e.vendor || t('Unknown vendor')}
          </p>
        </div>
        <span
          onClick={(ev) => ev.stopPropagation()}
          onKeyDown={(ev) => ev.stopPropagation()}
        >
          <CopyButton
            value={e.id}
            tooltip={t('Copy model name')}
            aria-label={t('Copy model name')}
            className='size-7'
            iconClassName='size-3.5'
          />
        </span>
      </CardHeader>
      <CardContent className='flex flex-1 flex-col gap-3'>
        <div className='flex min-w-0 flex-col gap-1.5'>
          <p
            className={cn(
              'line-clamp-2 text-[13px] leading-5 break-words',
              !e.description && 'text-muted-foreground',
            )}
          >
            {e.description ||
              t(
                'No description yet. An administrator can add one to the model catalogue.',
              )}
          </p>
          {e.tags.length > 0 && (
            <div className='flex flex-wrap gap-1' aria-label={t('Tags')}>
              {e.tags.slice(0, 3).map((tag) => (
                <Badge key={tag} variant='outline'>
                  {tag}
                </Badge>
              ))}
              {e.tags.length > 3 && (
                <Badge variant='outline'>+{e.tags.length - 3}</Badge>
              )}
            </div>
          )}
        </div>
        <div
          role='group'
          aria-label={t('Pricing')}
          className='mt-auto flex min-w-0 flex-col gap-1.5'
        >
          <div className='flex flex-wrap items-center gap-1.5'>
            <BillingModeBadge entry={e} />
            <CallableBadge entry={e} />
            {e.tokens !== null && (
              <Badge variant='outline'>
                {formatCompact(e.tokens)} {t('tokens · 7d')}
              </Badge>
            )}
          </div>
          <PriceGrid entry={e} />
        </div>
        {(e.groups.length > 0 || e.capabilities.length > 0) && (
          <dl className='grid min-w-0 grid-cols-1 gap-2 text-xs sm:grid-cols-2'>
            <ShortList label={t('Groups')} items={e.groups} max={1} />
            <ShortList
              label={t('Endpoints')}
              items={e.capabilities.map((c) => t(capabilityLabelKey(c)))}
              max={2}
            />
          </dl>
        )}
      </CardContent>
      <CardFooter className='mt-auto border-0 bg-transparent pt-0'>
        <PerfBadge entry={e} className='border-border/60 border-t pt-2'>
          <div className='flex items-center gap-1'>
            <CompareToggle id={e.id} />
            <Button variant='ghost' size='sm' type='button'>
              {t('Details')}
              <ChevronRight aria-hidden className='size-3.5' />
            </Button>
          </div>
        </PerfBadge>
      </CardFooter>
    </Card>
  );
});
