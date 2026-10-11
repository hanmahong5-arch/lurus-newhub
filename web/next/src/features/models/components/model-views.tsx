import type { ReactNode } from 'react';
import { useTranslation } from 'react-i18next';

import { Badge } from '@/components/ui/badge';
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table';

import {
  formatCompact,
  formatMs,
  formatPct,
  hasPrice,
  type ModelEntry,
} from '../lib/catalog';
import {
  CallableBadge,
  CapabilityLabel,
  ModalityLabel,
  OutputPriceValue,
  PriceLine,
  PriceValue,
  SearchUnitPriceValue,
  UsageUnitLabel,
} from './price';
import { usePriceFormatter } from './price-format';
import { VendorIcon } from './vendor-icon';

function PerfLine({ entry: e }: { entry: ModelEntry }) {
  const { t } = useTranslation();
  const id = `model-perf-${e.id}`;
  if (e.p50Ms == null && e.errorRate == null) {
    return (
      <span className='text-muted-foreground' data-testid={id}>
        {t('No traffic yet')}
      </span>
    );
  }
  if (!e.enoughSamples) {
    return (
      <span className='text-muted-foreground' data-testid={id}>
        {t('Too little traffic to measure')}
      </span>
    );
  }
  return (
    <span className='text-muted-foreground' data-testid={id}>
      p50 <b className='font-mono'>{formatMs(e.p50Ms)}</b> · {t('errors')}{' '}
      <b className='font-mono'>{formatPct(e.errorRate)}</b>
    </span>
  );
}

export function ModelCard(props: {
  entry: ModelEntry;
  onOpen: (id: string) => void;
}) {
  const { t } = useTranslation();
  const e = props.entry;
  return (
    <div
      role='button'
      tabIndex={0}
      data-testid={`model-card-${e.id}`}
      onClick={() => props.onOpen(e.id)}
      onKeyDown={(ev) => {
        if (ev.key === 'Enter' || ev.key === ' ') {
          ev.preventDefault();
          props.onOpen(e.id);
        }
      }}
      className='hover:border-ring focus-visible:ring-ring/50 bg-card cursor-pointer rounded-xl border p-4 transition-colors outline-none focus-visible:ring-3'
    >
      <div className='flex items-center gap-3'>
        <VendorIcon vendor={e.vendor} size={32} />
        <div className='min-w-0 flex-1'>
          <div className='flex items-center gap-2'>
            <span className='truncate font-mono text-sm font-semibold'>
              {e.id}
            </span>
            <CallableBadge entry={e} />
          </div>
          <div className='text-muted-foreground text-xs'>
            {e.vendor || t('Unknown vendor')}
          </div>
        </div>
      </div>
      <p
        className={
          'mt-2.5 line-clamp-2 text-sm' +
          (e.description ? '' : ' text-muted-foreground')
        }
      >
        {e.description ||
          t(
            'No description yet. An administrator can add one to the model catalogue.',
          )}
      </p>
      <div className='mt-3 flex flex-wrap items-center gap-2 text-xs'>
        <PriceLine entry={e} />
        {e.tokens !== null && (
          <Badge variant='outline'>
            {formatCompact(e.tokens)} {t('tokens · 7d')}
          </Badge>
        )}
        <PerfLine entry={e} />
        <span className='flex-1' />
        {e.capabilities.map((c) => (
          <Badge key={c} variant='outline'>
            <CapabilityLabel capability={c} />
          </Badge>
        ))}
      </div>
    </div>
  );
}

export function ModelTable(props: {
  entries: ModelEntry[];
  onOpen: (id: string) => void;
}) {
  const { t } = useTranslation();
  const fmt = usePriceFormatter();
  return (
    <div className='rounded-xl border'>
      <Table data-testid='models-table'>
        <TableHeader>
          <TableRow>
            <TableHead>{t('Model')}</TableHead>
            <TableHead>{t('Vendor')}</TableHead>
            <TableHead>{t('Modality')}</TableHead>
            <TableHead>{t('Billing unit')}</TableHead>
            <TableHead>{t('Input /M')}</TableHead>
            <TableHead>{t('Output /M')}</TableHead>
            <TableHead>{t('Cache read /M')}</TableHead>
            <TableHead>{t('Capabilities')}</TableHead>
            <TableHead>{t('tokens · 7d')}</TableHead>
            <TableHead>{t('p50 · 24h')}</TableHead>
            <TableHead>{t('Access')}</TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          {props.entries.map((e) => {
            const perCall = e.quotaType === 1;
            let priceCell: ReactNode;
            if (e.usageUnit === 'search_unit') {
              priceCell = <SearchUnitPriceValue value={e.searchUnitPrice} />;
            } else if (!perCall) priceCell = <PriceValue value={e.inputPerM} />;
            else if (hasPrice(e)) {
              priceCell = `${fmt(e.perCall)} ${t('per call')}`;
            } else priceCell = <PriceValue value={e.perCall} />;
            return (
              <TableRow
                key={e.id}
                data-testid={`model-row-${e.id}`}
                className='cursor-pointer'
                onClick={() => props.onOpen(e.id)}
              >
                <TableCell>
                  <span className='inline-flex items-center gap-2'>
                    <VendorIcon vendor={e.vendor} size={20} />
                    <span className='font-mono'>{e.id}</span>
                  </span>
                </TableCell>
                <TableCell className='text-muted-foreground'>
                  {e.vendor || '--'}
                </TableCell>
                <TableCell data-testid={`model-modality-${e.id}`}>
                  <ModalityLabel modality={e.modality} />
                </TableCell>
                <TableCell data-testid={`model-unit-${e.id}`}>
                  <UsageUnitLabel entry={e} />
                </TableCell>
                <TableCell className='font-mono'>{priceCell}</TableCell>
                <TableCell className='font-mono'>
                  {perCall ? '--' : <OutputPriceValue entry={e} />}
                </TableCell>
                <TableCell className='text-muted-foreground font-mono'>
                  {fmt(e.cacheReadPerM) ?? '--'}
                </TableCell>
                <TableCell>
                  {e.capabilities.length === 0
                    ? '--'
                    : e.capabilities.map((c, i) => (
                        <span key={c}>
                          {i > 0 && ' · '}
                          <CapabilityLabel capability={c} />
                        </span>
                      ))}
                </TableCell>
                <TableCell className='font-mono'>
                  {e.tokens === null ? '--' : formatCompact(e.tokens)}
                </TableCell>
                <TableCell className='font-mono'>
                  {e.enoughSamples ? formatMs(e.p50Ms) : '--'}
                </TableCell>
                <TableCell>
                  <CallableBadge entry={e} />
                </TableCell>
              </TableRow>
            );
          })}
        </TableBody>
      </Table>
    </div>
  );
}
