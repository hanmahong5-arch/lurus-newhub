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
import { Plus, Trash2 } from 'lucide-react'
import { useTranslation } from 'react-i18next'

import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'

import {
  MAX_TIERS,
  TIER_RATIO_FIELDS,
  emptyTierDraft,
  tierErrorKey,
  type EditErrors,
  type TierDraft,
  type TierRatioField,
} from '../lib/pricing'
import { validationText } from './messages'

/**
 * Context-length pricing tiers of one model: from `threshold` prompt tokens
 * up, the tier's ratios replace the flat ones. A blank ratio falls through to
 * the flat ratio (it is omitted from the request, never sent as 0).
 */
export function TierEditor(props: {
  model: string
  tiers: TierDraft[]
  errors: EditErrors
  readOnly: boolean
  onChange: (tiers: TierDraft[]) => void
}) {
  const { t } = useTranslation()
  const { model, tiers, errors, readOnly, onChange } = props

  const labels: Record<TierRatioField, string> = {
    model_ratio: t('Model ratio'),
    completion_ratio: t('Completion ratio'),
    cache_ratio: t('Cache ratio'),
  }

  const update = (idx: number, field: keyof TierDraft, value: string) =>
    onChange(tiers.map((tier, i) => (i === idx ? { ...tier, [field]: value } : tier)))

  const fieldError = (idx: number, field: string) => errors[tierErrorKey(idx, field)]

  return (
    <div className='space-y-2' data-testid={`context-tiers-editor-${model}`}>
      <p className='text-muted-foreground text-xs'>
        {t('From the threshold up, a tier replaces the flat ratios. A blank ratio keeps the flat one.')}
      </p>
      {tiers.length === 0 && (
        <p className='text-muted-foreground text-sm'>
          {t('No context tiers: the flat ratios apply at every length.')}
        </p>
      )}
      {tiers.map((tier, idx) => {
        const thresholdError = fieldError(idx, 'threshold_tokens')
        return (
          <div
            // oxlint-disable-next-line react/no-array-index-key -- tier drafts have no id; position is their identity
            key={idx}
            className='flex flex-wrap items-start gap-2 rounded-md border p-2'
          >
            <div className='grid gap-1'>
              <Input
                type='number'
                min={0}
                step={1}
                inputMode='numeric'
                className='h-8 w-32 font-mono text-xs'
                placeholder={t('Threshold tokens')}
                aria-label={t('Tier {{n}} threshold tokens', { n: idx + 1 })}
                aria-invalid={thresholdError !== undefined}
                disabled={readOnly}
                value={tier.threshold_tokens}
                onChange={(e) => update(idx, 'threshold_tokens', e.target.value)}
                data-testid={`tier-threshold-${model}-${idx}`}
              />
              {thresholdError && (
                <p className='text-destructive max-w-32 text-xs'>
                  {validationText(thresholdError, t)}
                </p>
              )}
            </div>
            {TIER_RATIO_FIELDS.map((field) => {
              const error = fieldError(idx, field)
              return (
                <div key={field} className='grid gap-1'>
                  <Input
                    type='number'
                    min={0}
                    step='any'
                    inputMode='decimal'
                    className='h-8 w-28 font-mono text-xs'
                    placeholder={labels[field]}
                    aria-label={t('Tier {{n}} {{field}}', {
                      n: idx + 1,
                      field: labels[field],
                    })}
                    aria-invalid={error !== undefined}
                    disabled={readOnly}
                    value={tier[field]}
                    onChange={(e) => update(idx, field, e.target.value)}
                    data-testid={`tier-${field}-${model}-${idx}`}
                  />
                  {error && (
                    <p className='text-destructive max-w-28 text-xs'>
                      {validationText(error, t)}
                    </p>
                  )}
                </div>
              )
            })}
            {!readOnly && (
              <Button
                type='button'
                variant='ghost'
                size='sm'
                className='h-8 gap-1 text-xs'
                onClick={() => onChange(tiers.filter((_, i) => i !== idx))}
                data-testid={`tier-remove-${model}-${idx}`}
                aria-label={t('Remove tier {{n}}', { n: idx + 1 })}
              >
                <Trash2 className='size-3.5' aria-hidden />
                {t('Remove')}
              </Button>
            )}
          </div>
        )
      })}
      {errors['tiers'] && (
        <p className='text-destructive text-xs'>
          {validationText(errors['tiers'], t)}
        </p>
      )}
      {!readOnly && (
        <Button
          type='button'
          variant='outline'
          size='sm'
          className='h-7 gap-1 text-xs'
          disabled={tiers.length >= MAX_TIERS}
          onClick={() => onChange([...tiers, emptyTierDraft()])}
          data-testid={`tier-add-${model}`}
        >
          <Plus className='size-3.5' aria-hidden />
          {t('Add tier')}
        </Button>
      )}
    </div>
  )
}
