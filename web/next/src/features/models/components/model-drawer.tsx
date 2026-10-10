import { useState } from 'react';
import { useTranslation } from 'react-i18next';

import { CopyButton } from '@/components/copy-button';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import {
  Sheet,
  SheetContent,
  SheetDescription,
  SheetHeader,
  SheetTitle,
} from '@/components/ui/sheet';

import {
  CAPABILITIES,
  formatCompact,
  formatMs,
  formatPct,
  hasPrice,
  type ModelEntry,
} from '../lib/catalog';
import { curlSnippet, pythonSnippet } from '../lib/snippets';
import {
  CallableBadge,
  CapabilityLabel,
  ModalityLabel,
  OutputPriceValue,
  PriceValue,
  SearchUnitPriceValue,
  UsageUnitLabel,
} from './price';
import { usePriceFormatter } from './price-format';
import { relayBase } from './relay-base';
import { VendorIcon } from './vendor-icon';

function Stat(props: {
  label: string;
  children: React.ReactNode;
  sub?: string;
}) {
  return (
    <div className='rounded-lg border px-3.5 py-3'>
      <div className='text-muted-foreground text-xs'>{props.label}</div>
      <div className='mt-1.5 font-mono text-lg font-semibold'>
        {props.children}
      </div>
      {props.sub && (
        <div className='text-muted-foreground mt-0.5 text-[11px]'>
          {props.sub}
        </div>
      )}
    </div>
  );
}

function PriceStats(props: { entry: ModelEntry }) {
  const { t } = useTranslation();
  const fmt = usePriceFormatter();
  const e = props.entry;
  if (e.usageUnit === 'search_unit') {
    return (
      <Stat label={t('Price per search unit')}>
        <SearchUnitPriceValue value={e.searchUnitPrice} />
      </Stat>
    );
  }
  if (e.quotaType === 1) {
    return (
      <Stat label={t('Price per call')}>
        <PriceValue value={e.perCall} />
      </Stat>
    );
  }
  return (
    <>
      <Stat label={t('Input /M')} sub={t('per 1M tokens')}>
        <PriceValue value={e.inputPerM} />
      </Stat>
      <Stat label={t('Output /M')} sub={t('per 1M tokens')}>
        <OutputPriceValue entry={e} />
      </Stat>
      <Stat label={t('Cache read /M')}>{fmt(e.cacheReadPerM) ?? '--'}</Stat>
    </>
  );
}

export function ModelDrawer(props: {
  entry: ModelEntry | null;
  onClose: () => void;
}) {
  const { t } = useTranslation();
  const [tab, setTab] = useState<'curl' | 'python'>('curl');
  const e = props.entry;
  const base = relayBase();
  let code = '';
  if (e) code = tab === 'curl' ? curlSnippet(e, base) : pythonSnippet(e, base);

  return (
    <Sheet
      open={e != null}
      onOpenChange={(open) => {
        if (!open) props.onClose();
      }}
    >
      <SheetContent
        side='right'
        className='w-full overflow-y-auto sm:max-w-xl'
        data-testid='model-drawer'
      >
        {e && (
          <>
            <SheetHeader>
              <div className='flex items-center gap-3'>
                <VendorIcon vendor={e.vendor} size={36} />
                <div className='min-w-0'>
                  <SheetTitle className='truncate font-mono'>{e.id}</SheetTitle>
                  <SheetDescription>
                    {e.vendor || t('Unknown vendor')}
                  </SheetDescription>
                </div>
              </div>
            </SheetHeader>
            <div className='space-y-4 px-4 pb-6'>
              <div className='flex flex-wrap gap-2'>
                <CallableBadge entry={e} />
                {e.tags.map((tag) => (
                  <Badge key={tag} variant='outline'>
                    {tag}
                  </Badge>
                ))}
              </div>
              <p className='text-sm leading-relaxed'>
                {e.description ||
                  t(
                    'No description yet. An administrator can add one to the model catalogue.',
                  )}
              </p>

              <div
                className='grid grid-cols-2 gap-2.5'
                data-testid='model-drawer-prices'
              >
                <Stat label={t('Modality')}>
                  <ModalityLabel modality={e.modality} />
                </Stat>
                <Stat label={t('Billing unit')}>
                  <UsageUnitLabel entry={e} />
                </Stat>
                <PriceStats entry={e} />
                {e.tokens !== null && (
                  <Stat
                    label={t('tokens · 7d')}
                    sub={`${formatCompact(e.requests ?? 0)} ${t('requests')}`}
                  >
                    {formatCompact(e.tokens)}
                  </Stat>
                )}
              </div>
              {hasPrice(e) && e.usageUnit !== 'search_unit' && (
                <p className='text-muted-foreground text-xs'>
                  {t('Prices include your group multiplier.')}
                </p>
              )}

              {(e.p50Ms != null || e.errorRate != null) && (
                <div
                  className='grid grid-cols-3 gap-2.5'
                  data-testid='model-drawer-perf'
                >
                  <Stat label='p50'>
                    {e.enoughSamples ? formatMs(e.p50Ms) : '--'}
                  </Stat>
                  <Stat label='p95'>
                    {e.enoughSamples ? formatMs(e.p95Ms) : '--'}
                  </Stat>
                  <Stat
                    label={t('errors')}
                    sub={
                      e.enoughSamples
                        ? t('last 24h, your tenant')
                        : t('Too little traffic to measure')
                    }
                  >
                    {e.enoughSamples ? formatPct(e.errorRate) : '--'}
                  </Stat>
                </div>
              )}

              <div>
                <div className='text-muted-foreground text-xs font-medium'>
                  {t('Capabilities')}
                </div>
                {e.capabilities.length === 0 && (
                  <div className='text-muted-foreground mt-1.5 text-sm'>--</div>
                )}
                {e.capabilities.map((c) => (
                  <div
                    key={c}
                    className='flex justify-between border-b py-1.5 text-sm'
                  >
                    <span>
                      <CapabilityLabel capability={c} />
                    </span>
                    <span className='text-muted-foreground font-mono text-xs'>
                      {CAPABILITIES[c]?.path ?? ''}
                    </span>
                  </div>
                ))}
              </div>

              <div>
                <div className='flex items-center gap-1.5'>
                  <span className='text-muted-foreground flex-1 text-xs font-medium'>
                    {t('Quick start')}
                  </span>
                  {(['curl', 'python'] as const).map((k) => (
                    <Button
                      key={k}
                      size='sm'
                      variant={tab === k ? 'default' : 'outline'}
                      onClick={() => setTab(k)}
                    >
                      {k === 'curl' ? 'cURL' : 'Python'}
                    </Button>
                  ))}
                  <CopyButton
                    value={code}
                    variant='outline'
                    tooltip={t('Copy to clipboard')}
                    aria-label={t('Copy code')}
                  />
                </div>
                <pre
                  className='bg-muted mt-2 overflow-x-auto rounded-lg p-3 font-mono text-xs leading-relaxed'
                  data-testid='model-drawer-code'
                >
                  {code}
                </pre>
              </div>
            </div>
          </>
        )}
      </SheetContent>
    </Sheet>
  );
}
