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
import { useState, type ReactNode } from 'react';
import { useTranslation } from 'react-i18next';

import { CopyButton } from '@/components/copy-button';
import { sideDrawerContentClassName } from '@/components/drawer-layout';
import { Badge } from '@/components/ui/badge';
import {
  Sheet,
  SheetContent,
  SheetDescription,
  SheetHeader,
  SheetTitle,
} from '@/components/ui/sheet';
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs';
import { ToggleGroup, ToggleGroupItem } from '@/components/ui/toggle-group';

import {
  CAPABILITIES,
  formatCompact,
  formatPct,
  hasPrice,
  perUnit,
  type ModelEntry,
} from '../lib/catalog';
import {
  formatLatency,
  PERF_WINDOWS,
  type PerfWindow,
} from '../lib/perf';
import {
  buildSnippet,
  SNIPPET_LANG_LABEL,
  SNIPPET_LANGS,
  snippetEndpoints,
  type SnippetLang,
} from '../lib/snippets';
import { SuccessRate } from './perf-badge';
import {
  perfWindowLabelKey,
  useNoMeasurement,
  usePerfWindow,
} from './perf-window';
import {
  BillingModeBadge,
  CallableBadge,
  CapabilityLabel,
  ModalityLabel,
  OutputPriceValue,
  PriceValue,
  SearchUnitPriceValue,
  UsageUnitLabel,
} from './price';
import { usePriceFormatter } from './price-format';
import { usePriceUnit } from './price-unit';
import { relayBase } from './relay-base';
import { VendorIcon } from './vendor-icon';

function Stat(props: { label: string; children: ReactNode; sub?: string }) {
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

function SectionTitle(props: { children: ReactNode }) {
  return (
    <h3 className='text-foreground text-sm font-semibold'>{props.children}</h3>
  );
}

function PriceStats(props: { entry: ModelEntry }) {
  const { t } = useTranslation();
  const fmt = usePriceFormatter();
  const unit = usePriceUnit();
  const e = props.entry;
  const unitLabel = unit === 'K' ? '1K' : '1M';
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
      <Stat
        label={t('Input / {{unit}}', { unit: unitLabel })}
        sub={t('per {{unit}} tokens', { unit: unitLabel })}
      >
        <PriceValue value={perUnit(e.inputPerM, unit)} />
      </Stat>
      <Stat
        label={t('Output / {{unit}}', { unit: unitLabel })}
        sub={t('per {{unit}} tokens', { unit: unitLabel })}
      >
        <OutputPriceValue entry={e} />
      </Stat>
      <Stat label={t('Cache read / {{unit}}', { unit: unitLabel })}>
        {fmt(perUnit(e.cacheReadPerM, unit)) ?? '--'}
      </Stat>
    </>
  );
}

/** Same model under every group the caller may use, relative to the shown one. */
function GroupPricing(props: {
  entry: ModelEntry;
  groupRatios: Record<string, number>;
  activeRatio: number;
}) {
  const { t } = useTranslation();
  const fmt = usePriceFormatter();
  const unit = usePriceUnit();
  const e = props.entry;
  const rows = e.groups
    .filter((g) => Number.isFinite(props.groupRatios[g]))
    .map((g) => ({ name: g, ratio: props.groupRatios[g] }));
  // Without a non-zero reference multiplier the other groups cannot be derived.
  if (
    rows.length === 0 ||
    !(props.activeRatio > 0) ||
    !hasPrice(e) ||
    e.usageUnit === 'search_unit'
  ) {
    return null;
  }
  const scale = (v: number | null, ratio: number) =>
    v == null ? null : (v / props.activeRatio) * ratio;
  const perCall = e.quotaType === 1;
  return (
    <div data-testid='model-drawer-groups'>
      <SectionTitle>{t('Pricing by group')}</SectionTitle>
      <div className='mt-2 overflow-x-auto rounded-lg border'>
        <table className='w-full text-sm'>
          <thead className='text-muted-foreground bg-muted/40 text-xs'>
            <tr>
              <th className='px-3 py-2 text-left font-medium'>{t('Group')}</th>
              <th className='px-3 py-2 text-right font-medium'>
                {t('Multiplier')}
              </th>
              <th className='px-3 py-2 text-right font-medium'>
                {perCall ? t('Price per call') : t('Input')}
              </th>
              {!perCall && (
                <th className='px-3 py-2 text-right font-medium'>
                  {t('Output')}
                </th>
              )}
            </tr>
          </thead>
          <tbody>
            {rows.map((r) => (
              <tr key={r.name} className='border-t'>
                <td className='px-3 py-2'>{r.name}</td>
                <td className='px-3 py-2 text-right font-mono'>
                  x{Number(r.ratio.toFixed(3))}
                </td>
                <td className='px-3 py-2 text-right font-mono'>
                  {perCall
                    ? fmt(scale(e.perCall, r.ratio))
                    : fmt(perUnit(scale(e.inputPerM, r.ratio), unit))}
                </td>
                {!perCall && (
                  <td className='px-3 py-2 text-right font-mono'>
                    {e.inputOnly
                      ? t('Not billed')
                      : (fmt(perUnit(scale(e.outputPerM, r.ratio), unit)) ??
                        '--')}
                  </td>
                )}
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </div>
  );
}

function Overview(props: {
  entry: ModelEntry;
  groupRatios: Record<string, number>;
  activeRatio: number;
}) {
  const { t } = useTranslation();
  const e = props.entry;
  return (
    <div className='space-y-5'>
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
      {!hasPrice(e) && e.usageUnit !== 'search_unit' && (
        <p
          className='text-muted-foreground text-xs'
          data-testid='model-drawer-unpriced'
        >
          {t(
            'Nobody has priced this model yet. Until an administrator sets a price it is not free, and the relay may refuse it.',
          )}
        </p>
      )}
      {hasPrice(e) && e.usageUnit !== 'search_unit' && (
        <p className='text-muted-foreground text-xs'>
          {t('Prices include your group multiplier.')}
        </p>
      )}

      <GroupPricing
        entry={e}
        groupRatios={props.groupRatios}
        activeRatio={props.activeRatio}
      />

      <div>
        <SectionTitle>{t('Capabilities')}</SectionTitle>
        {e.capabilities.length === 0 && (
          <div className='text-muted-foreground mt-1.5 text-sm'>--</div>
        )}
        {e.capabilities.map((c) => (
          <div
            key={c}
            className='flex justify-between gap-3 border-b py-1.5 text-sm'
          >
            <span>
              <CapabilityLabel capability={c} />
            </span>
            <span className='text-muted-foreground font-mono text-xs break-all'>
              {CAPABILITIES[c]?.path ?? ''}
            </span>
          </div>
        ))}
      </div>

      {e.groups.length > 0 && (
        <div>
          <SectionTitle>{t('Groups')}</SectionTitle>
          <div className='mt-2 flex flex-wrap gap-1.5'>
            {e.groups.map((g) => (
              <Badge key={g} variant='outline'>
                {g}
              </Badge>
            ))}
          </div>
        </div>
      )}
    </div>
  );
}

function Performance(props: {
  entry: ModelEntry;
  onWindowChange: (w: PerfWindow) => void;
}) {
  const { t } = useTranslation();
  const e = props.entry;
  const windowHours = usePerfWindow();
  const reason = useNoMeasurement(e);
  const measured = reason === null;
  return (
    <div className='space-y-4' data-testid='model-drawer-perf'>
      <div className='flex flex-wrap items-center justify-between gap-2'>
        <SectionTitle>{t('Latency and reliability')}</SectionTitle>
        <ToggleGroup
          value={[String(windowHours)]}
          onValueChange={(values) => {
            const w = Number(values[0]);
            if ((PERF_WINDOWS as readonly number[]).includes(w)) {
              props.onWindowChange(w as PerfWindow);
            }
          }}
          variant='outline'
          size='sm'
          aria-label={t('Latency window')}
        >
          <ToggleGroupItem value='1'>1h</ToggleGroupItem>
          <ToggleGroupItem value='24'>24h</ToggleGroupItem>
          <ToggleGroupItem value='168'>7d</ToggleGroupItem>
        </ToggleGroup>
      </div>
      {measured ? (
        <div className='grid grid-cols-3 gap-2.5'>
          <Stat label={t('Success')} sub={t(perfWindowLabelKey(windowHours))}>
            <SuccessRate entry={e} />
          </Stat>
          <Stat label='p50' sub={t(perfWindowLabelKey(windowHours))}>
            {formatLatency(e.p50Ms)}
          </Stat>
          <Stat label='p95' sub={t(perfWindowLabelKey(windowHours))}>
            {formatLatency(e.p95Ms)}
          </Stat>
        </div>
      ) : (
        <p className='text-muted-foreground text-sm'>{reason}</p>
      )}
      {measured && (
        <p className='text-muted-foreground text-xs'>
          {t('Error rate')}: {formatPct(e.errorRate)}
        </p>
      )}
      <p className='text-muted-foreground text-xs'>
        {t(
          'Measured on your own tenant traffic. Latency is end to end per request; first-token time and throughput are not recorded.',
        )}
      </p>
    </div>
  );
}

function Api(props: { entry: ModelEntry }) {
  const { t } = useTranslation();
  const e = props.entry;
  const endpoints = snippetEndpoints(e);
  const [endpoint, setEndpoint] = useState(endpoints[0]);
  const [lang, setLang] = useState<SnippetLang>('curl');
  const active = endpoints.includes(endpoint) ? endpoint : endpoints[0];
  const code = buildSnippet(e, active, lang, relayBase());
  return (
    <div className='space-y-3'>
      <div className='flex items-center gap-1.5'>
        <SectionTitle>{t('Quick start')}</SectionTitle>
        <span className='flex-1' />
        <CopyButton
          value={code}
          variant='outline'
          tooltip={t('Copy to clipboard')}
          aria-label={t('Copy code')}
        />
      </div>
      {endpoints.length > 1 && (
        <Tabs value={active} onValueChange={(v) => setEndpoint(String(v))}>
          <TabsList className='h-auto flex-wrap'>
            {endpoints.map((c) => (
              <TabsTrigger key={c} value={c} className='text-xs'>
                <CapabilityLabel capability={c} />
              </TabsTrigger>
            ))}
          </TabsList>
        </Tabs>
      )}
      <Tabs value={lang} onValueChange={(v) => setLang(v as SnippetLang)}>
        <TabsList>
          {SNIPPET_LANGS.map((l) => (
            <TabsTrigger key={l} value={l} className='text-xs'>
              {SNIPPET_LANG_LABEL[l]}
            </TabsTrigger>
          ))}
        </TabsList>
      </Tabs>
      <pre
        className='bg-muted overflow-x-auto rounded-lg p-3 font-mono text-xs leading-relaxed'
        data-testid='model-drawer-code'
      >
        {code}
      </pre>
      <p className='text-muted-foreground text-xs'>
        {t('Create an API key on the Keys page and export it as')}{' '}
        <code className='font-mono'>LURUS_API_KEY</code>.
      </p>
    </div>
  );
}

export function ModelDrawer(props: {
  entry: ModelEntry | null;
  onClose: () => void;
  groupRatios: Record<string, number>;
  activeRatio: number;
  onPerfWindowChange: (w: PerfWindow) => void;
}) {
  const { t } = useTranslation();
  const e = props.entry;

  return (
    <Sheet
      open={e != null}
      onOpenChange={(open) => {
        if (!open) props.onClose();
      }}
    >
      <SheetContent
        side='right'
        className={sideDrawerContentClassName('sm:max-w-2xl')}
        data-testid='model-drawer'
      >
        {e && (
          <>
            <SheetHeader className='border-border/70 border-b px-4 py-4 text-start sm:px-6'>
              <div className='flex items-start gap-3 pr-8'>
                <VendorIcon vendor={e.vendor} icon={e.icon} size={36} />
                <div className='min-w-0 flex-1'>
                  <div className='flex items-center gap-1.5'>
                    <SheetTitle className='truncate font-mono'>
                      {e.id}
                    </SheetTitle>
                    <CopyButton
                      value={e.id}
                      className='size-6'
                      iconClassName='size-3'
                      tooltip={t('Copy model name')}
                      aria-label={t('Copy model name')}
                    />
                  </div>
                  <SheetDescription>
                    {e.vendor || t('Unknown vendor')}
                  </SheetDescription>
                </div>
              </div>
              <div className='mt-2 flex flex-wrap gap-1.5'>
                <BillingModeBadge entry={e} />
                <CallableBadge entry={e} />
                {e.tags.map((tag) => (
                  <Badge key={tag} variant='outline'>
                    {tag}
                  </Badge>
                ))}
              </div>
              <p className='mt-2 text-sm leading-relaxed'>
                {e.description ||
                  t(
                    'No description yet. An administrator can add one to the model catalogue.',
                  )}
              </p>
            </SheetHeader>
            <div className='min-h-0 flex-1 overflow-y-auto px-4 py-4 sm:px-6'>
              <Tabs defaultValue='overview' className='gap-4'>
                <TabsList className='grid w-full grid-cols-3'>
                  <TabsTrigger value='overview'>{t('Overview')}</TabsTrigger>
                  <TabsTrigger value='performance'>
                    {t('Performance')}
                  </TabsTrigger>
                  <TabsTrigger value='api'>{t('API')}</TabsTrigger>
                </TabsList>
                <TabsContent value='overview' className='outline-none'>
                  <Overview
                    entry={e}
                    groupRatios={props.groupRatios}
                    activeRatio={props.activeRatio}
                  />
                </TabsContent>
                <TabsContent value='performance' className='outline-none'>
                  <Performance
                    entry={e}
                    onWindowChange={props.onPerfWindowChange}
                  />
                </TabsContent>
                <TabsContent value='api' className='outline-none'>
                  <Api key={e.id} entry={e} />
                </TabsContent>
              </Tabs>
            </div>
          </>
        )}
      </SheetContent>
    </Sheet>
  );
}
