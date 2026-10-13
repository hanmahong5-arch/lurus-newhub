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
import { ChevronDown, RotateCcw } from 'lucide-react';
import { memo, useMemo, type ReactNode } from 'react';
import { useTranslation } from 'react-i18next';

import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import {
  Collapsible,
  CollapsibleContent,
  CollapsibleTrigger,
} from '@/components/ui/collapsible';
import { cn } from '@/lib/utils';

import {
  capabilityFacets,
  capabilityLabelKey,
  modalityFacets,
  modalityLabelKey,
  PRICE_TYPES,
  priceTypeCounts,
  priceTypeLabelKey,
  tagFacets,
  vendorFacets,
  type ModelEntry,
} from '../lib/catalog';
import { toggleIn, type Filters } from '../lib/filter-state';
import { VendorIcon } from './vendor-icon';

export interface GroupOption {
  name: string;
  /** Multiplier the group applies to every price. */
  ratio: number;
}

function formatRatio(ratio: number): string {
  const s = Number.isInteger(ratio)
    ? String(ratio)
    : ratio.toFixed(3).replace(/0+$/, '').replace(/\.$/, '');
  return `x${s}`;
}

interface Chip {
  value: string;
  label: ReactNode;
  count?: number;
  suffix?: string;
  icon?: ReactNode;
  testid?: string;
}

function FilterChip(props: {
  chip: Chip;
  active: boolean;
  onClick: () => void;
}) {
  const { chip, active } = props;
  return (
    <Button
      type='button'
      variant={active ? 'secondary' : 'outline'}
      size='sm'
      onClick={props.onClick}
      aria-pressed={active}
      data-testid={chip.testid}
      className='h-auto max-w-full gap-1.5 px-2 py-1 text-xs'
    >
      {chip.icon && <span className='shrink-0'>{chip.icon}</span>}
      <span className='truncate'>{chip.label}</span>
      {(chip.suffix || chip.count != null) && (
        <span
          className={cn(
            'rounded-md px-1.5 py-0.5 text-[12px]',
            active
              ? 'bg-background text-foreground'
              : 'bg-muted text-muted-foreground',
          )}
        >
          {chip.suffix ?? chip.count}
        </span>
      )}
    </Button>
  );
}

function Section(props: {
  title: string;
  chips: Chip[];
  isActive: (value: string) => boolean;
  onToggle: (value: string) => void;
}) {
  if (props.chips.length === 0) return null;
  return (
    <Collapsible
      defaultOpen
      className='border-border/70 border-b pb-3 last:border-b-0'
    >
      <CollapsibleTrigger className='group flex w-full items-center justify-between py-2.5 text-left'>
        <span className='text-foreground text-sm font-semibold'>
          {props.title}
        </span>
        <ChevronDown className='text-muted-foreground size-4 transition-transform group-data-[panel-open]:rotate-180' />
      </CollapsibleTrigger>
      <CollapsibleContent>
        <div className='flex flex-wrap gap-1.5'>
          {props.chips.map((chip) => (
            <FilterChip
              key={chip.value || '_'}
              chip={chip}
              active={props.isActive(chip.value)}
              onClick={() => props.onToggle(chip.value)}
            />
          ))}
        </div>
      </CollapsibleContent>
    </Collapsible>
  );
}

export interface FilterSidebarProps {
  entries: ModelEntry[];
  filters: Filters;
  onFiltersChange: (next: Filters) => void;
  /** Groups the caller may price under; empty hides the section. */
  groups: GroupOption[];
  /** "" = the caller's own group. */
  group: string;
  onGroupChange: (group: string) => void;
  onReset: () => void;
  activeCount: number;
  className?: string;
}

/** Facet rail: availability, group, vendor, modality, billing, endpoint, tag. */
export const FilterSidebar = memo(function FilterSidebar(
  props: FilterSidebarProps,
) {
  const { t } = useTranslation();
  const { entries, filters: f, onFiltersChange: set } = props;

  const vendors = useMemo(() => vendorFacets(entries), [entries]);
  const caps = useMemo(() => capabilityFacets(entries), [entries]);
  const tags = useMemo(() => tagFacets(entries), [entries]);
  const modalities = useMemo(() => modalityFacets(entries), [entries]);
  const kinds = useMemo(() => priceTypeCounts(entries), [entries]);

  return (
    <aside
      className={cn('bg-card rounded-xl border p-3', props.className)}
      data-testid='models-rail'
      aria-label={t('Filter')}
    >
      <div className='mb-2.5 flex items-center justify-between gap-2'>
        <div>
          <h2 className='text-foreground text-sm font-bold'>{t('Filter')}</h2>
          <p className='text-muted-foreground mt-1 text-xs'>
            {t('Refine models by vendor, group, type, and tags.')}
          </p>
        </div>
        <Button
          type='button'
          variant='ghost'
          size='sm'
          onClick={props.onReset}
          disabled={props.activeCount === 0}
          className='h-7 gap-1.5 px-2 text-xs'
          data-testid='models-reset'
        >
          <RotateCcw className='size-3.5' />
          {t('Reset')}
        </Button>
      </div>

      {props.activeCount > 0 && (
        <Badge variant='secondary' className='mb-3'>
          {t('{{n}} filters active', { n: props.activeCount })}
        </Badge>
      )}

      <label className='border-border/70 flex cursor-pointer items-center gap-2 border-b pt-1 pb-3 text-sm'>
        <input
          type='checkbox'
          checked={f.routableOnly}
          onChange={() => set({ ...f, routableOnly: !f.routableOnly })}
          data-testid='models-callable-only'
          className='size-4'
        />
        {t('Callable by me only')}
      </label>

      <div className='space-y-1'>
        <Section
          title={t('Price group')}
          chips={
            props.groups.length === 0
              ? []
              : [
                  {
                    value: '',
                    label: t('My group'),
                    testid: 'group-facet-mine',
                  },
                  ...props.groups.map((g) => ({
                    value: g.name,
                    label: g.name,
                    suffix: formatRatio(g.ratio),
                    testid: `group-facet-${g.name}`,
                  })),
                ]
          }
          isActive={(v) => props.group === v}
          onToggle={(v) => props.onGroupChange(v === props.group ? '' : v)}
        />
        <Section
          title={t('Vendor')}
          chips={vendors.map(({ vendor, count }) => ({
            value: vendor,
            label: vendor || t('Unknown vendor'),
            count,
            icon: <VendorIcon vendor={vendor} size={14} />,
            testid: `vendor-facet-${vendor || 'unknown'}`,
          }))}
          isActive={(v) => f.vendors.includes(v)}
          onToggle={(v) => set({ ...f, vendors: toggleIn(f.vendors, v) })}
        />
        <Section
          title={t('Modality')}
          chips={modalities.map(({ modality, count }) => ({
            value: modality,
            label: t(modalityLabelKey(modality)),
            count,
            testid: `modality-facet-${modality}`,
          }))}
          isActive={(v) => f.modalities.includes(v)}
          onToggle={(v) => set({ ...f, modalities: toggleIn(f.modalities, v) })}
        />
        <Section
          title={t('Pricing type')}
          chips={PRICE_TYPES.filter((p) => kinds[p] > 0).map((p) => ({
            value: p,
            label: t(priceTypeLabelKey(p)),
            count: kinds[p],
            testid: `price-facet-${p}`,
          }))}
          isActive={(v) => f.priceType === v}
          onToggle={(v) =>
            set({
              ...f,
              priceType:
                f.priceType === v ? '' : (v as Filters['priceType']),
            })
          }
        />
        <Section
          title={t('Endpoint type')}
          chips={caps.map(({ capability, count }) => ({
            value: capability,
            label: t(capabilityLabelKey(capability)),
            count,
            testid: `cap-facet-${capability}`,
          }))}
          isActive={(v) => f.capabilities.includes(v)}
          onToggle={(v) =>
            set({ ...f, capabilities: toggleIn(f.capabilities, v) })
          }
        />
        <Section
          title={t('Model tags')}
          chips={tags.map(({ tag, count }) => ({
            value: tag,
            label: tag,
            count,
            testid: `tag-facet-${tag}`,
          }))}
          isActive={(v) => f.tags.includes(v)}
          onToggle={(v) => set({ ...f, tags: toggleIn(f.tags, v) })}
        />
      </div>
    </aside>
  );
});
