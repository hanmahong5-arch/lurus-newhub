import { useMemo, useState } from 'react';
import { useTranslation } from 'react-i18next';

import { EmptyState } from '@/components/empty-state';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import {
  NativeSelect,
  NativeSelectOption,
} from '@/components/ui/native-select';

import {
  capabilityFacets,
  filterCatalog,
  sortCatalog,
  vendorFacets,
  type ModelEntry,
  type SortKey,
} from '../lib/catalog';
import { ModelDrawer } from './model-drawer';
import { ModelCard, ModelTable } from './model-views';
import { CapabilityLabel } from './price';
import { VendorIcon } from './vendor-icon';

const toggle = (list: string[], v: string) =>
  list.includes(v) ? list.filter((x) => x !== v) : [...list, v];

function FacetRow(props: {
  testid: string;
  checked: boolean;
  onChange: () => void;
  label: React.ReactNode;
  count: number;
  icon?: React.ReactNode;
}) {
  return (
    <label
      data-testid={props.testid}
      className='flex cursor-pointer items-center gap-2 px-1 py-1 text-sm'
    >
      <input
        type='checkbox'
        checked={props.checked}
        onChange={props.onChange}
        className='size-4'
      />
      {props.icon}
      <span className='min-w-0 flex-1 truncate'>{props.label}</span>
      <span className='text-muted-foreground font-mono text-xs'>
        {props.count}
      </span>
    </label>
  );
}

/** Facet rail + card/table list + detail drawer over merged entries. */
export function Marketplace(props: { entries: ModelEntry[] }) {
  const { t } = useTranslation();
  const { entries } = props;
  const [q, setQ] = useState('');
  const [vendors, setVendors] = useState<string[]>([]);
  const [caps, setCaps] = useState<string[]>([]);
  const [routableOnly, setRoutableOnly] = useState(false);
  // Without usage data "most used" would rank on nothing: hide it.
  const usageKnown = entries.some((e) => e.tokens !== null);
  const [sort, setSort] = useState<SortKey>(usageKnown ? 'popular' : 'name');
  const [view, setView] = useState<'list' | 'table'>('list');
  const [openId, setOpenId] = useState<string | null>(null);

  const vFacets = useMemo(() => vendorFacets(entries), [entries]);
  const cFacets = useMemo(() => capabilityFacets(entries), [entries]);
  const shown = useMemo(
    () =>
      sortCatalog(
        filterCatalog(entries, {
          q,
          vendors,
          capabilities: caps,
          routableOnly,
        }),
        usageKnown || sort !== 'popular' ? sort : 'name',
      ),
    [entries, q, vendors, caps, routableOnly, sort, usageKnown],
  );
  const open = openId ? (entries.find((e) => e.id === openId) ?? null) : null;
  const filtered = Boolean(
    q.trim() || vendors.length || caps.length || routableOnly,
  );

  const results =
    view === 'table' ? (
      <ModelTable entries={shown} onOpen={setOpenId} />
    ) : (
      <div className='grid gap-3'>
        {shown.map((e) => (
          <ModelCard key={e.id} entry={e} onOpen={setOpenId} />
        ))}
      </div>
    );
  return (
    <div className='grid gap-6 lg:grid-cols-[220px_1fr]'>
      <aside className='space-y-4' data-testid='models-rail'>
        <Input
          type='search'
          placeholder={t('Search models…')}
          aria-label={t('Search models…')}
          value={q}
          onChange={(ev) => setQ(ev.target.value)}
          data-testid='models-search'
        />
        <label className='flex cursor-pointer items-center gap-2 text-sm'>
          <input
            type='checkbox'
            checked={routableOnly}
            onChange={() => setRoutableOnly((v) => !v)}
            data-testid='models-callable-only'
            className='size-4'
          />
          {t('Callable by me only')}
        </label>

        <div>
          <div className='text-muted-foreground mb-1 text-xs font-medium'>
            {t('Vendor')}
          </div>
          {vFacets.map(({ vendor, count }) => (
            <FacetRow
              key={vendor || '_'}
              testid={`vendor-facet-${vendor || 'unknown'}`}
              checked={vendors.includes(vendor)}
              onChange={() => setVendors((l) => toggle(l, vendor))}
              icon={<VendorIcon vendor={vendor} size={18} />}
              label={vendor || t('Unknown vendor')}
              count={count}
            />
          ))}
        </div>

        {cFacets.length > 0 && (
          <div>
            <div className='text-muted-foreground mb-1 text-xs font-medium'>
              {t('Capabilities')}
            </div>
            {cFacets.map(({ capability, count }) => (
              <FacetRow
                key={capability}
                testid={`cap-facet-${capability}`}
                checked={caps.includes(capability)}
                onChange={() => setCaps((l) => toggle(l, capability))}
                label={<CapabilityLabel capability={capability} />}
                count={count}
              />
            ))}
          </div>
        )}
      </aside>

      <section className='min-w-0 space-y-3'>
        <div className='flex flex-wrap items-center gap-2'>
          <span className='text-muted-foreground text-sm'>
            {filtered
              ? t('{{n}} of {{total}}', {
                  n: shown.length,
                  total: entries.length,
                })
              : t('{{n}} models', { n: entries.length })}
          </span>
          <span className='flex-1' />
          <NativeSelect
            size='sm'
            value={usageKnown || sort !== 'popular' ? sort : 'name'}
            onChange={(ev) => setSort(ev.target.value as SortKey)}
            data-testid='models-sort'
            aria-label={t('Sort')}
          >
            {usageKnown && (
              <NativeSelectOption value='popular'>
                {t('Most used')}
              </NativeSelectOption>
            )}
            <NativeSelectOption value='name'>{t('Name')}</NativeSelectOption>
            <NativeSelectOption value='input_asc'>
              {t('Input price ↑')}
            </NativeSelectOption>
            <NativeSelectOption value='output_asc'>
              {t('Output price ↑')}
            </NativeSelectOption>
            <NativeSelectOption value='fastest'>
              {t('Fastest (p50)')}
            </NativeSelectOption>
          </NativeSelect>
          <div className='flex gap-1'>
            {(['list', 'table'] as const).map((v) => (
              <Button
                key={v}
                size='sm'
                variant={view === v ? 'default' : 'outline'}
                aria-pressed={view === v}
                onClick={() => setView(v)}
                data-testid={`models-view-${v}`}
              >
                {v === 'list' ? t('Cards') : t('Table')}
              </Button>
            ))}
          </div>
        </div>

        {shown.length === 0 ? (
          <div data-testid='models-empty'>
            <EmptyState
              bordered
              title={
                entries.length === 0
                  ? t('No models yet')
                  : t('No model matches these filters')
              }
              description={
                entries.length === 0
                  ? t('Add an upstream channel and its models appear here.')
                  : undefined
              }
            />
          </div>
        ) : (
          results
        )}
      </section>

      <ModelDrawer entry={open} onClose={() => setOpenId(null)} />
    </div>
  );
}
