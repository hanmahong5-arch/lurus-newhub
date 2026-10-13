import type { ReactNode } from 'react';
import { useTranslation } from 'react-i18next';

import { Badge } from '@/components/ui/badge';
import { cn } from '@/lib/utils';

import {
  capabilityLabelKey,
  hasPrice,
  modalityLabelKey,
  perUnit,
  priceTypeLabelKey,
  priceTypeOf,
  type ModelEntry,
} from '../lib/catalog';
import { usePriceFormatter } from './price-format';
import { usePriceUnit } from './price-unit';

/**
 * One price value. Zero, missing or unparsable is "Price not configured",
 * never $0 and never "free": nobody priced the model.
 */
export function PriceValue(props: { value: number | null | undefined }) {
  const { t } = useTranslation();
  const fmt = usePriceFormatter();
  const text = fmt(props.value);
  return (
    text ?? (
      <span className='text-muted-foreground font-sans text-xs font-normal'>
        {t('Price not configured')}
      </span>
    )
  );
}

/**
 * Search-unit price. null/undefined = not configured (billing falls back to
 * tokens); a literal 0 = deliberately free. The two must never look alike.
 */
export function SearchUnitPriceValue(props: {
  value: number | null | undefined;
}) {
  const { t } = useTranslation();
  const fmt = usePriceFormatter();
  const v = props.value;
  if (v === null || v === undefined || !Number.isFinite(v)) {
    return (
      <span className='text-muted-foreground' data-testid='search-unit-unset'>
        {t('Not configured')}
      </span>
    );
  }
  if (v === 0) {
    return <span data-testid='search-unit-free'>{t('Free')}</span>;
  }
  return <span data-testid='search-unit-price'>{fmt(v)}</span>;
}

export function ModalityLabel(props: { modality: string }) {
  const { t } = useTranslation();
  if (!props.modality) return <span>--</span>;
  return <span>{t(modalityLabelKey(props.modality))}</span>;
}

/** Billing unit label: token or search unit. */
export function UsageUnitLabel(props: { entry: ModelEntry }) {
  const { t } = useTranslation();
  const search = props.entry.usageUnit === 'search_unit';
  return <span>{search ? t('Per search unit') : t('Per token')}</span>;
}

/** Output column: an input-only model bills no output, which is not "unpriced". */
export function OutputPriceValue(props: { entry: ModelEntry }) {
  const { t } = useTranslation();
  const unit = usePriceUnit();
  if (props.entry.inputOnly && hasPrice(props.entry)) {
    return <span className='text-muted-foreground'>{t('Not billed')}</span>;
  }
  return <PriceValue value={perUnit(props.entry.outputPerM, unit)} />;
}

/** How the model is billed; an unpriced model says so instead of a price. */
export function BillingModeBadge(props: {
  entry: ModelEntry;
  className?: string;
}) {
  const { t } = useTranslation();
  const kind = priceTypeOf(props.entry);
  return (
    <Badge
      variant={kind === 'unpriced' ? 'outline' : 'secondary'}
      className={props.className}
      data-testid={`model-billing-${props.entry.id}`}
    >
      {t(priceTypeLabelKey(kind))}
    </Badge>
  );
}

function PriceCell(props: {
  label: string;
  className?: string;
  children: ReactNode;
}) {
  return (
    <div className={cn('flex min-w-0 flex-col gap-1', props.className)}>
      <span className='text-muted-foreground text-xs'>{props.label}</span>
      <span className='flex flex-wrap items-baseline gap-x-1 font-mono text-sm font-semibold tabular-nums'>
        {props.children}
      </span>
    </div>
  );
}

/** Per-unit suffix, e.g. " / 1M". */
function UnitSuffix(props: { children: ReactNode }) {
  return (
    <span className='text-muted-foreground text-xs font-normal whitespace-nowrap'>
      {' '}
      / {props.children}
    </span>
  );
}

/** Price summary used on cards: input / output / cached, or a single price. */
export function PriceGrid(props: { entry: ModelEntry }) {
  const { t } = useTranslation();
  const fmt = usePriceFormatter();
  const unit = usePriceUnit();
  const e = props.entry;
  const unitLabel = unit === 'K' ? '1K' : '1M';
  const id = `model-price-${e.id}`;

  if (e.usageUnit === 'search_unit') {
    return (
      <div data-testid={id}>
        <PriceCell label={t('Price per search unit')} className='col-span-full'>
          <SearchUnitPriceValue value={e.searchUnitPrice} />
          <UnitSuffix>{t('search unit')}</UnitSuffix>
        </PriceCell>
      </div>
    );
  }
  if (!hasPrice(e)) {
    return (
      <div data-testid={id} className='text-muted-foreground text-sm'>
        {t('Price not configured')}
      </div>
    );
  }
  if (e.quotaType === 1) {
    return (
      <div data-testid={id}>
        <PriceCell label={t('Price per call')}>
          {fmt(e.perCall)}
          <UnitSuffix>{t('request')}</UnitSuffix>
        </PriceCell>
      </div>
    );
  }
  const input = perUnit(e.inputPerM, unit);
  const output = perUnit(e.outputPerM, unit);
  const cache = perUnit(e.cacheReadPerM, unit);
  return (
    <div
      data-testid={id}
      className='grid grid-cols-[repeat(auto-fit,minmax(88px,1fr))] gap-x-3 gap-y-2'
    >
      <PriceCell label={t('Input')}>
        {fmt(input)}
        <UnitSuffix>{unitLabel}</UnitSuffix>
      </PriceCell>
      {e.inputOnly ? (
        <PriceCell label={t('Output')}>
          <span className='text-muted-foreground font-sans text-xs font-normal'>
            {t('Output not billed')}
          </span>
        </PriceCell>
      ) : (
        fmt(output) && (
          <PriceCell label={t('Output')}>
            {fmt(output)}
            <UnitSuffix>{unitLabel}</UnitSuffix>
          </PriceCell>
        )
      )}
      {fmt(cache) && (
        <PriceCell label={t('Cached')}>
          {fmt(cache)}
          <UnitSuffix>{unitLabel}</UnitSuffix>
        </PriceCell>
      )}
    </div>
  );
}

export function CapabilityLabel(props: { capability: string }) {
  const { t } = useTranslation();
  return <>{t(capabilityLabelKey(props.capability))}</>;
}

export function CallableBadge(props: { entry: ModelEntry }) {
  const { t } = useTranslation();
  return props.entry.routable ? (
    <Badge variant='secondary' data-testid={`model-callable-${props.entry.id}`}>
      {t('Callable')}
    </Badge>
  ) : (
    <Badge
      variant='outline'
      data-testid={`model-not-callable-${props.entry.id}`}
    >
      {t('Not in your groups')}
    </Badge>
  );
}
