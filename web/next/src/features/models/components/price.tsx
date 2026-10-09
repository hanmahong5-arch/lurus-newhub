import type { ReactNode } from 'react';
import { useTranslation } from 'react-i18next';

import { Badge } from '@/components/ui/badge';

import { capabilityLabelKey, hasPrice, type ModelEntry } from '../lib/catalog';
import { usePriceFormatter } from './price-format';

/** One price value; zero, missing or unparsable is "Unpriced", never $0. */
export function PriceValue(props: { value: number | null | undefined }) {
  const { t } = useTranslation();
  const fmt = usePriceFormatter();
  const text = fmt(props.value);
  return text ?? <span className='text-muted-foreground'>{t('Unpriced')}</span>;
}

/** Output column: an input-only model bills no output, which is not "unpriced". */
export function OutputPriceValue(props: { entry: ModelEntry }) {
  const { t } = useTranslation();
  if (props.entry.inputOnly && hasPrice(props.entry)) {
    return <span className='text-muted-foreground'>{t('Not billed')}</span>;
  }
  return <PriceValue value={props.entry.outputPerM} />;
}

/** Compact price strip used on cards. */
export function PriceLine(props: { entry: ModelEntry }) {
  const { t } = useTranslation();
  const fmt = usePriceFormatter();
  const e = props.entry;
  if (!hasPrice(e)) {
    return (
      <Badge variant='outline' data-testid={`model-price-${e.id}`}>
        {t('Unpriced')}
      </Badge>
    );
  }
  if (e.quotaType === 1) {
    return (
      <Badge variant='secondary' data-testid={`model-price-${e.id}`}>
        {fmt(e.perCall)} {t('per call')}
      </Badge>
    );
  }
  let outputBadge: ReactNode = null;
  if (e.inputOnly) {
    outputBadge = <Badge variant='outline'>{t('Output not billed')}</Badge>;
  } else if (fmt(e.outputPerM)) {
    outputBadge = (
      <Badge variant='secondary'>
        {fmt(e.outputPerM)} {t('/M output')}
      </Badge>
    );
  }
  return (
    <span
      className='flex flex-wrap items-center gap-1.5'
      data-testid={`model-price-${e.id}`}
    >
      <Badge variant='secondary'>
        {fmt(e.inputPerM)} {t('/M input')}
      </Badge>
      {outputBadge}
      {fmt(e.cacheReadPerM) && (
        <Badge variant='outline'>
          {t('cache read')} {fmt(e.cacheReadPerM)}
          {t('/M')}
        </Badge>
      )}
    </span>
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
