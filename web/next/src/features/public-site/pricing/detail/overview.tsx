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
import { useTranslation } from 'react-i18next'

import { StaticDataTable } from '@/components/data-table'
import { GroupBadge } from '@/components/group-badge'
import { StatusBadge } from '@/components/status-badge'
import { cn } from '@/lib/utils'

import type { TokenUnit } from '../constants'
import {
  groupMultiplier,
  pricingTypeOf,
  priceView,
  type PublicModel,
} from '../lib/model'
import { PriceSummary, TypeBadge } from '../components/price-cells'
import {
  endpointLabel,
  modalityLabel,
  typeLabelKey,
  unitLabel,
  useAmountFormatter,
} from '../lib/labels'

function SectionTitle(props: { children: React.ReactNode }) {
  return (
    <h2 className='text-muted-foreground mb-3 text-xs font-semibold tracking-wider uppercase'>
      {props.children}
    </h2>
  )
}

function Metric(props: { label: string; children: React.ReactNode }) {
  return (
    <div className='bg-card/60 min-w-0 rounded-lg border px-3 py-2.5'>
      <div className='text-muted-foreground text-xs'>{props.label}</div>
      <div className='mt-1 min-w-0 truncate text-sm font-medium'>
        {props.children}
      </div>
    </div>
  )
}

function InfoRow(props: { label: string; children: React.ReactNode }) {
  return (
    <div className='flex min-w-0 flex-col gap-1.5'>
      <dt className='text-muted-foreground text-xs'>{props.label}</dt>
      <dd className='flex min-w-0 flex-wrap gap-1.5 text-sm'>{props.children}</dd>
    </div>
  )
}

/** One group's price cells: input/output(/cached) or a single price. */
function GroupPriceTable(props: { model: PublicModel; unit: TokenUnit }) {
  const { t } = useTranslation()
  const fmt = useAmountFormatter()
  const { model, unit } = props
  const type = pricingTypeOf(model.entry)
  const per = unitLabel(unit)
  const hasCache = model.entry.cacheReadPerM !== null

  const cell = (group: string, pick: 'input' | 'output' | 'cached' | 'price') => {
    const view = priceView(model, model.ratios[group], unit)
    if (view.kind === 'unpriced') return t('Unpriced')
    if (view.kind === 'unavailable') return t('Price unavailable')
    if (view.kind === 'call' || view.kind === 'search') {
      return fmt(view.price) ?? t('Not configured')
    }
    if (pick === 'output' && !view.outputBilled) return t('Not billed')
    const values = { input: view.input, output: view.output, cached: view.cache }
    return fmt(values[pick === 'price' ? 'input' : pick]) ?? '--'
  }

  const columns = [
    {
      id: 'group',
      header: t('Group'),
      cell: (g: string) => <GroupBadge group={g} size='sm' />,
    },
    {
      id: 'ratio',
      header: t('Ratio'),
      cellClassName: 'text-muted-foreground font-mono',
      cell: (g: string) => `${model.ratios[g]}x`,
    },
    ...(type === 'token'
      ? [
          {
            id: 'input',
            header: `${t('Input')} / ${per}`,
            className: 'text-end',
            cellClassName: 'text-end font-mono',
            cell: (g: string) => cell(g, 'input'),
          },
          {
            id: 'output',
            header: `${t('Output')} / ${per}`,
            className: 'text-end',
            cellClassName: 'text-end font-mono',
            cell: (g: string) => cell(g, 'output'),
          },
          ...(hasCache
            ? [
                {
                  id: 'cached',
                  header: `${t('Cached')} / ${per}`,
                  className: 'text-end',
                  cellClassName: 'text-end font-mono',
                  cell: (g: string) => cell(g, 'cached'),
                },
              ]
            : []),
        ]
      : [
          {
            id: 'price',
            header:
              type === 'call'
                ? `${t('Price')} / ${t('request')}`
                : `${t('Price')} / ${t('search unit')}`,
            className: 'text-end',
            cellClassName: 'text-end font-mono',
            cell: (g: string) => cell(g, 'price'),
          },
        ]),
  ]

  return (
    <StaticDataTable
      columns={columns}
      data={model.groups}
      getRowKey={(g) => g}
    />
  )
}

export function Overview(props: { model: PublicModel; unit: TokenUnit }) {
  const { t } = useTranslation()
  const { model, unit } = props
  const type = pricingTypeOf(model.entry)
  const best = groupMultiplier(model)
  const view = priceView(model, best, unit)
  const endpointNames = model.endpoints.map((e) => endpointLabel(t, e))

  return (
    <div className='space-y-6'>
      <div className='grid grid-cols-2 gap-2 sm:grid-cols-4'>
        <Metric label={t('Billing')}>{t(typeLabelKey(type))}</Metric>
        <Metric label={t('Modality')}>
          {model.modality ? modalityLabel(t, model.modality) : '--'}
        </Metric>
        <Metric label={t('Vendor')}>{model.vendor || '--'}</Metric>
        <Metric label={t('Endpoints')}>
          {endpointNames.length > 0 ? endpointNames.length : '--'}
        </Metric>
      </div>

      <section className='bg-card/60 space-y-5 rounded-xl border p-4 shadow-sm'>
        <div>
          <SectionTitle>{t('Base Price')}</SectionTitle>
          <PriceSummary view={view} unit={unit} />
          {model.groups.length > 1 && (
            <p className='text-muted-foreground mt-2 text-xs'>
              {t('Shown at the lowest group ratio, {{ratio}}x.', {
                ratio: best,
              })}
            </p>
          )}
        </div>

        {model.groups.length > 0 && (
          <div>
            <SectionTitle>{t('Pricing by Group')}</SectionTitle>
            <GroupPriceTable model={model} unit={unit} />
            {type === 'token' && (
              <p className='text-muted-foreground/70 mt-1.5 text-[11px]'>
                {t('Prices shown per {{unit}} tokens', { unit: unitLabel(unit) })}
              </p>
            )}
          </div>
        )}
      </section>

      <section>
        <SectionTitle>{t('Model')}</SectionTitle>
        <dl className={cn('grid gap-x-8 gap-y-4 sm:grid-cols-2')}>
          <InfoRow label={t('Vendor')}>{model.vendor || '--'}</InfoRow>
          <InfoRow label={t('Type')}>
            <TypeBadge model={model} />
          </InfoRow>
          {model.groups.length > 0 && (
            <InfoRow label={t('Groups')}>
              {model.groups.map((g) => (
                <GroupBadge key={g} group={g} size='sm' />
              ))}
            </InfoRow>
          )}
          {model.endpoints.length > 0 && (
            <InfoRow label={t('Endpoints')}>
              {model.endpoints.map((e) => (
                <StatusBadge
                  key={e}
                  label={endpointLabel(t, e)}
                  autoColor={e}
                  size='sm'
                  copyable={false}
                />
              ))}
            </InfoRow>
          )}
          {model.tags.length > 0 && (
            <InfoRow label={t('Tags')}>
              {model.tags.map((tag) => (
                <StatusBadge
                  key={tag}
                  label={tag}
                  autoColor={tag}
                  size='sm'
                  copyable={false}
                />
              ))}
            </InfoRow>
          )}
        </dl>
      </section>
    </div>
  )
}
