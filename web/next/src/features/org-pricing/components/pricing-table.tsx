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
import { RotateCcw } from 'lucide-react'
import { Fragment, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table'
import { cn } from '@/lib/utils'

import {
  baselineValue,
  fieldValue,
  isUnpriced,
  tiersOf,
  type EditErrors,
  type Edits,
  type PricingRow,
  type ScalarField,
  type TierDraft,
} from '../lib/pricing'
import { validationText } from './messages'
import { TierEditor } from './tier-editor'
import { TierToggle } from './tier-toggle'

/** The explicit-free vs not-configured distinction of a search-unit price. */
function SearchUnitPrice(props: { value: number | undefined }) {
  const { t } = useTranslation()
  if (props.value === undefined) {
    return (
      <span className='text-muted-foreground' data-testid='search-unit-unset'>
        {t('Not configured')}
      </span>
    )
  }
  if (props.value === 0) {
    return <span data-testid='search-unit-free'>{t('Free')}</span>
  }
  return (
    <span className='font-mono' data-testid='search-unit-price'>
      {props.value}
    </span>
  )
}

export interface PricingTableProps {
  rows: PricingRow[]
  edits: Edits
  errors: Record<string, EditErrors>
  readOnly: boolean
  /** Per-call column only exists when some visible row is per-call. */
  showModelPrice: boolean
  /** Search-unit column only exists when some row carries the field. */
  showSearchUnit: boolean
  onScalar: (model: string, field: ScalarField, value: string) => void
  onTiers: (row: PricingRow, tiers: TierDraft[]) => void
  onReset: (model: string) => void
}

export function PricingTable(props: PricingTableProps) {
  const { t } = useTranslation()
  const { rows, edits, errors, readOnly, showModelPrice, showSearchUnit } = props
  // Expand/collapse of the tier editor is UI state only; it never persists.
  const [expanded, setExpanded] = useState<Record<string, boolean>>({})

  const columnCount =
    8 + (showModelPrice ? 1 : 0) + (showSearchUnit ? 1 : 0) + (readOnly ? 0 : 1)

  const labels: Record<ScalarField, string> = {
    model_ratio: t('Model ratio'),
    completion_ratio: t('Completion ratio'),
    model_price: t('Model price'),
    cache_ratio: t('Cache ratio'),
  }

  /** One editable (or read-only) number cell. */
  const scalarCell = (row: PricingRow, field: ScalarField, step: string) => {
    const edit = edits[row.model_name]
    const value = fieldValue(row, edit, field)
    const error = errors[row.model_name]?.[field]
    const unpriced =
      isUnpriced(row) &&
      (field === 'model_ratio' || field === 'model_price') &&
      baselineValue(row, field) === ''
    if (readOnly) {
      if (value === '') {
        return unpriced ? (
          <Badge variant='warning'>{t('Not priced')}</Badge>
        ) : (
          <span className='text-muted-foreground'>-</span>
        )
      }
      return <span className='font-mono'>{value}</span>
    }
    return (
      <div className='grid gap-1'>
        <Input
          type='number'
          min={0}
          step={step}
          inputMode='decimal'
          className='h-8 w-28 font-mono text-xs'
          aria-label={`${labels[field]}: ${row.model_name}`}
          aria-invalid={error !== undefined}
          placeholder={unpriced ? t('Not priced') : undefined}
          value={value}
          onChange={(e) => props.onScalar(row.model_name, field, e.target.value)}
          data-testid={`field-${field}-${row.model_name}`}
        />
        {error && (
          <p className='text-destructive max-w-28 text-xs whitespace-normal'>
            {validationText(error, t)}
          </p>
        )}
      </div>
    )
  }

  const notApplicable = <span className='text-muted-foreground'>-</span>

  return (
    <Table data-testid='pricing-table'>
      <TableHeader>
        <TableRow>
          <TableHead>{t('Model name')}</TableHead>
          <TableHead>{t('Vendor')}</TableHead>
          <TableHead>{t('Billing type')}</TableHead>
          <TableHead>{labels.model_ratio}</TableHead>
          <TableHead>{labels.completion_ratio}</TableHead>
          {showModelPrice && (
            <TableHead data-testid='pricing-th-model-price'>
              {labels.model_price}
            </TableHead>
          )}
          <TableHead>{labels.cache_ratio}</TableHead>
          <TableHead>{t('Context tiers')}</TableHead>
          {showSearchUnit && <TableHead>{t('Search unit price')}</TableHead>}
          <TableHead>{t('Enabled groups')}</TableHead>
          {!readOnly && (
            <TableHead>
              <span className='sr-only'>{t('Actions')}</span>
            </TableHead>
          )}
        </TableRow>
      </TableHeader>
      <TableBody>
        {rows.map((row) => {
          const edit = edits[row.model_name]
          const tiers = tiersOf(row, edit)
          const perCall = row.quota_type === 1
          const open = expanded[row.model_name] === true
          const edited = edit !== undefined
          return (
            <Fragment key={row.model_name}>
              <TableRow
                data-testid={`pricing-row-${row.model_name}`}
                data-edited={edited ? 'true' : 'false'}
                className={cn(edited && 'bg-primary/5')}
              >
                <TableCell className='font-mono text-xs'>
                  <div className='flex items-center gap-2'>
                    <span>{row.model_name}</span>
                    {isUnpriced(row) && (
                      <Badge variant='warning' data-testid={`unpriced-${row.model_name}`}>
                        {t('Unpriced')}
                      </Badge>
                    )}
                  </div>
                </TableCell>
                <TableCell>{row.vendor || '-'}</TableCell>
                <TableCell>
                  <Badge variant='secondary'>
                    {perCall ? t('Per-call price') : t('Ratio-based')}
                  </Badge>
                </TableCell>
                <TableCell>
                  {perCall ? notApplicable : scalarCell(row, 'model_ratio', 'any')}
                </TableCell>
                <TableCell>
                  {perCall
                    ? notApplicable
                    : scalarCell(row, 'completion_ratio', 'any')}
                </TableCell>
                {showModelPrice && (
                  <TableCell>
                    {perCall ? scalarCell(row, 'model_price', 'any') : notApplicable}
                  </TableCell>
                )}
                <TableCell>{scalarCell(row, 'cache_ratio', 'any')}</TableCell>
                <TableCell>
                  {/* Tiers only apply to token-based models. */}
                  {perCall ? (
                    notApplicable
                  ) : (
                    <TierToggle
                      count={tiers.length}
                      expanded={open}
                      readOnly={readOnly}
                      testId={`context-tiers-toggle-${row.model_name}`}
                      onClick={() =>
                        setExpanded((prev) => ({
                          ...prev,
                          [row.model_name]: !prev[row.model_name],
                        }))
                      }
                    />
                  )}
                </TableCell>
                {showSearchUnit && (
                  <TableCell>
                    <SearchUnitPrice value={row.search_unit_price} />
                  </TableCell>
                )}
                <TableCell className='text-muted-foreground max-w-48 truncate text-xs'>
                  {row.enable_groups.length > 0 ? row.enable_groups.join(', ') : '-'}
                </TableCell>
                {!readOnly && (
                  <TableCell>
                    {edited && (
                      <Button
                        type='button'
                        variant='ghost'
                        size='icon-sm'
                        aria-label={t('Discard edits of {{model}}', {
                          model: row.model_name,
                        })}
                        title={t('Discard edits')}
                        onClick={() => props.onReset(row.model_name)}
                        data-testid={`reset-${row.model_name}`}
                      >
                        <RotateCcw aria-hidden />
                      </Button>
                    )}
                  </TableCell>
                )}
              </TableRow>
              {!perCall && open && (
                <TableRow className='bg-muted/30 hover:bg-muted/30 h-auto'>
                  <TableCell colSpan={columnCount} className='whitespace-normal'>
                    <TierEditor
                      model={row.model_name}
                      tiers={tiers}
                      errors={errors[row.model_name] ?? {}}
                      readOnly={readOnly}
                      onChange={(next) => props.onTiers(row, next)}
                    />
                  </TableCell>
                </TableRow>
              )}
            </Fragment>
          )
        })}
      </TableBody>
    </Table>
  )
}
