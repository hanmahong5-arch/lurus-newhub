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
import { ChevronDown, RotateCcw } from 'lucide-react'
import { memo, type ReactNode } from 'react'
import { useTranslation } from 'react-i18next'

import { getLobeIcon } from '@/components/lobe-icon'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import {
  Collapsible,
  CollapsibleContent,
  CollapsibleTrigger,
} from '@/components/ui/collapsible'
import { cn } from '@/lib/utils'

import type { PublicVendor } from '../../api'
import type { PricingSearch } from '../constants'
import type { Facet } from '../lib/filters'
import { endpointLabel, modalityLabel, typeLabelKey } from '../lib/labels'
import type { PricingType } from '../lib/model'

interface Option {
  value: string | undefined
  label: string
  count?: number
  suffix?: string
  icon?: ReactNode
}

interface SidebarFacets {
  vendors: Facet[]
  tags: Facet[]
  endpoints: Facet[]
  modalities: Facet[]
  types: Facet[]
  groups: Facet[]
}

export interface PricingSidebarProps {
  search: PricingSearch
  facets: SidebarFacets
  total: number
  vendors: PublicVendor[]
  groupRatios: Record<string, number>
  hasActiveFilters: boolean
  onChange: (patch: Partial<PricingSearch>) => void
  onReset: () => void
  className?: string
}

function formatRatio(ratio: number | undefined): string | undefined {
  if (ratio === undefined) return undefined
  const text = Number.isInteger(ratio)
    ? String(ratio)
    : ratio.toFixed(3).replace(/0+$/, '').replace(/\.$/, '')
  return `x${text}`
}

function Chip(props: {
  option: Option
  active: boolean
  onClick: () => void
}) {
  const { option } = props
  return (
    <Button
      type='button'
      variant={props.active ? 'secondary' : 'outline'}
      size='sm'
      onClick={props.onClick}
      aria-pressed={props.active}
      title={option.label}
      className='h-auto max-w-full gap-1.5 px-2 py-1 text-xs'
    >
      {option.icon && <span className='shrink-0'>{option.icon}</span>}
      <span className='truncate'>{option.label}</span>
      {(option.suffix !== undefined || option.count !== undefined) && (
        <span
          className={cn(
            'rounded-md px-1.5 py-0.5 text-[12px]',
            props.active
              ? 'bg-background text-foreground'
              : 'bg-muted text-muted-foreground'
          )}
        >
          {option.suffix ?? option.count}
        </span>
      )}
    </Button>
  )
}

function Section(props: {
  title: string
  value: string | undefined
  options: Option[]
  onChange: (value: string | undefined) => void
}) {
  // A section with nothing to choose between is noise.
  if (props.options.length <= 1) return null
  return (
    <Collapsible
      defaultOpen
      className='border-border/70 border-b pb-3 last:border-b-0'
    >
      <CollapsibleTrigger className='group flex w-full items-center justify-between py-2.5 text-start'>
        <span className='text-foreground text-sm font-semibold'>
          {props.title}
        </span>
        <ChevronDown
          aria-hidden
          className='text-muted-foreground size-4 transition-transform group-data-[panel-open]:rotate-180'
        />
      </CollapsibleTrigger>
      <CollapsibleContent>
        <div className='flex flex-wrap gap-1.5'>
          {props.options.map((option) => (
            <Chip
              key={option.value ?? 'all'}
              option={option}
              active={props.value === option.value}
              onClick={() => props.onChange(option.value)}
            />
          ))}
        </div>
      </CollapsibleContent>
    </Collapsible>
  )
}

export const PricingSidebar = memo(function PricingSidebar(
  props: PricingSidebarProps
) {
  const { t } = useTranslation()
  const { facets, search, total } = props
  const icons = new Map(props.vendors.map((v) => [v.name, v.icon]))

  const all = (label: string): Option => ({
    value: undefined,
    label,
    count: total,
  })

  const groupOptions: Option[] = [
    { value: undefined, label: t('All Groups') },
    ...facets.groups.map((g) => ({
      value: g.value,
      label: g.value,
      suffix: formatRatio(props.groupRatios[g.value]),
    })),
  ]
  const vendorOptions: Option[] = [
    all(t('All Vendors')),
    ...facets.vendors.map((v) => {
      const icon = icons.get(v.value)
      return {
        value: v.value,
        label: v.value,
        count: v.count,
        icon: icon ? getLobeIcon(icon, 14) : undefined,
      }
    }),
  ]
  const modalityOptions: Option[] = [
    all(t('All Modalities')),
    ...facets.modalities.map((m) => ({
      value: m.value,
      label: modalityLabel(t, m.value),
      count: m.count,
    })),
  ]
  const typeOptions: Option[] = [
    all(t('All Models')),
    ...facets.types.map((x) => ({
      value: x.value,
      label: t(typeLabelKey(x.value as PricingType)),
      count: x.count,
    })),
  ]
  const endpointOptions: Option[] = [
    all(t('All Types')),
    ...facets.endpoints.map((x) => ({
      value: x.value,
      label: endpointLabel(t, x.value),
      count: x.count,
    })),
  ]
  const tagOptions: Option[] = [
    all(t('All Tags')),
    ...facets.tags.map((x) => ({
      value: x.value,
      label: x.value,
      count: x.count,
    })),
  ]

  return (
    <aside
      aria-label={t('Filter')}
      className={cn('bg-card rounded-xl border p-3', props.className)}
    >
      <div className='mb-2.5 flex items-center justify-between gap-2'>
        <div>
          <h2 className='text-foreground text-sm font-bold'>{t('Filter')}</h2>
          <p className='text-muted-foreground mt-1 text-xs'>
            {t('Refine models by vendor, group, type and tags.')}
          </p>
        </div>
        <Button
          type='button'
          variant='ghost'
          size='sm'
          onClick={props.onReset}
          disabled={!props.hasActiveFilters}
          className='h-7 gap-1.5 px-2 text-xs'
        >
          <RotateCcw className='size-3.5' />
          {t('Reset')}
        </Button>
      </div>

      {props.hasActiveFilters && (
        <Badge variant='secondary' className='mb-3'>
          {t('Filters active')}
        </Badge>
      )}

      <div className='space-y-1'>
        <Section
          title={t('Groups')}
          value={search.group}
          options={groupOptions}
          onChange={(group) => props.onChange({ group })}
        />
        <Section
          title={t('Vendor')}
          value={search.vendor}
          options={vendorOptions}
          onChange={(vendor) => props.onChange({ vendor })}
        />
        <Section
          title={t('Modality')}
          value={search.modality}
          options={modalityOptions}
          onChange={(modality) => props.onChange({ modality })}
        />
        <Section
          title={t('Pricing Type')}
          value={search.type}
          options={typeOptions}
          onChange={(type) =>
            props.onChange({ type: type as PricingSearch['type'] })
          }
        />
        <Section
          title={t('Endpoint Type')}
          value={search.endpoint}
          options={endpointOptions}
          onChange={(endpoint) => props.onChange({ endpoint })}
        />
        <Section
          title={t('Model Tags')}
          value={search.tag}
          options={tagOptions}
          onChange={(tag) => props.onChange({ tag })}
        />
      </div>
    </aside>
  )
})
