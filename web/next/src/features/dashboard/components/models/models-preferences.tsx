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
import { Save, Settings2 } from 'lucide-react'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'

import { Dialog } from '@/components/dialog'
import { Button } from '@/components/ui/button'
import { Label } from '@/components/ui/label'
import {
  Select,
  SelectContent,
  SelectGroup,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import type { TimeGranularity } from '@/lib/time'

import {
  CONSUMPTION_DISTRIBUTION_CHART_OPTIONS,
  MODEL_ANALYTICS_CHART_OPTIONS,
  TIME_GRANULARITY_OPTIONS,
  TIME_RANGE_PRESETS,
} from '../../lib/constants'
import type {
  ConsumptionDistributionChartType,
  DashboardChartPreferences,
  ModelAnalyticsChartTab,
} from '../../lib/types'

interface Option {
  value: string
  label: string
}

function Field(props: {
  id: string
  label: string
  placeholder: string
  value: string
  options: Option[]
  onChange: (value: string) => void
}) {
  return (
    <div className='grid gap-1.5'>
      <Label htmlFor={props.id}>{props.label}</Label>
      <Select
        items={props.options}
        value={props.value}
        onValueChange={(v) => props.onChange(String(v))}
      >
        <SelectTrigger id={props.id}>
          <SelectValue placeholder={props.placeholder} />
        </SelectTrigger>
        <SelectContent alignItemWithTrigger={false}>
          <SelectGroup>
            {props.options.map((o) => (
              <SelectItem key={o.value} value={o.value}>
                {o.label}
              </SelectItem>
            ))}
          </SelectGroup>
        </SelectContent>
      </Select>
    </div>
  )
}

export interface ModelsChartPreferencesProps {
  preferences: DashboardChartPreferences
  onSave: (preferences: DashboardChartPreferences) => void
}

/** Per-browser defaults for the range, bucket size and chart types. */
export function ModelsChartPreferences(props: ModelsChartPreferencesProps) {
  const { t } = useTranslation()
  const [open, setOpen] = useState(false)
  const [draft, setDraft] = useState(props.preferences)

  const handleOpenChange = (next: boolean) => {
    if (next) setDraft(props.preferences)
    setOpen(next)
  }

  return (
    <Dialog
      open={open}
      onOpenChange={handleOpenChange}
      trigger={
        <Button variant='outline' size='sm'>
          <Settings2 className='me-2 size-4' />
          {t('Preferences')}
        </Button>
      }
      title={t('Model Analytics Defaults')}
      description={t('Set default ranges and charts for model analytics.')}
      contentClassName='sm:max-w-md'
      contentHeight='auto'
      bodyClassName='grid gap-3'
      footer={
        <Button
          type='button'
          onClick={() => {
            props.onSave(draft)
            setOpen(false)
          }}
        >
          <Save className='me-2 size-4' />
          {t('Save Preferences')}
        </Button>
      }
    >
      <Field
        id='default-time-range'
        label={t('Default range')}
        placeholder={t('Select default range')}
        value={String(draft.defaultTimeRangeDays)}
        options={TIME_RANGE_PRESETS.map((o) => ({
          value: String(o.days),
          label: t(o.label),
        }))}
        onChange={(v) =>
          setDraft((p) => ({ ...p, defaultTimeRangeDays: Number(v) }))
        }
      />
      <Field
        id='default-time-granularity'
        label={t('Default time granularity')}
        placeholder={t('Select time granularity')}
        value={draft.defaultTimeGranularity}
        options={TIME_GRANULARITY_OPTIONS.map((o) => ({
          value: o.value,
          label: t(o.label),
        }))}
        onChange={(v) =>
          setDraft((p) => ({
            ...p,
            defaultTimeGranularity: v as TimeGranularity,
          }))
        }
      />
      <Field
        id='consumption-distribution-chart'
        label={t('Default consumption chart')}
        placeholder={t('Select default chart')}
        value={draft.consumptionDistributionChart}
        options={CONSUMPTION_DISTRIBUTION_CHART_OPTIONS.map((o) => ({
          value: o.value,
          label: t(o.labelKey),
        }))}
        onChange={(v) =>
          setDraft((p) => ({
            ...p,
            consumptionDistributionChart: v as ConsumptionDistributionChartType,
          }))
        }
      />
      <Field
        id='model-analytics-chart'
        label={t('Default model call chart')}
        placeholder={t('Select default chart')}
        value={draft.modelAnalyticsChart}
        options={MODEL_ANALYTICS_CHART_OPTIONS.map((o) => ({
          value: o.value,
          label: t(o.labelKey),
        }))}
        onChange={(v) =>
          setDraft((p) => ({
            ...p,
            modelAnalyticsChart: v as ModelAnalyticsChartTab,
          }))
        }
      />
    </Dialog>
  )
}
