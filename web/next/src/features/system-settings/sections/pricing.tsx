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
import { useQueryClient } from '@tanstack/react-query'
import { RotateCcw } from 'lucide-react'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { ConfirmDialog } from '@/components/confirm-dialog'
import { Button } from '@/components/ui/button'
import { Tabs, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { handleServerError } from '@/lib/handle-server-error'

import { resetModelRatios } from '../api'
import {
  GroupGroupRatioEditor,
  GroupRatioEditor,
  UsableGroupsEditor,
} from '../components/group-editors'
import { ModelPriceTable } from '../components/model-price-table'
import { OptionForm } from '../components/option-form'
import { UpstreamSync } from '../components/upstream-sync'
import { useOptionDraft } from '../hooks/use-option-draft'
import { SYSTEM_OPTIONS_QUERY_KEY } from '../hooks/use-system-options'
import type { OptionField } from '../lib/option-fields'
import {
  validateGroupGroupRatios,
  validateGroupRatios,
  validateUsableGroups,
} from '../lib/validators'
import {
  parseNumberMap,
  validateNumberMap,
  type PricingKey,
} from '../lib/pricing'

type PricingTab = 'models' | 'sync' | 'json'

const SCALAR_KEYS = new Set(['ExposeRatioEnabled', 'ModelFallbackMarkup'])

function jsonMap(
  key: PricingKey | 'SearchUnitPrice',
  label: string,
  description: string
): OptionField {
  return {
    kind: 'json',
    key,
    label,
    description,
    expect: 'object',
    validate: validateNumberMap,
    heightClassName: 'h-48 min-h-48 max-h-48',
  }
}

const PRICING_FIELDS: OptionField[] = [
  {
    kind: 'switch',
    key: 'ExposeRatioEnabled',
    label: 'Expose ratios publicly',
    description:
      'Serve the ratio list at /api/ratio_config so other gateways can sync from this one',
  },
  {
    kind: 'number',
    key: 'ModelFallbackMarkup',
    label: 'Fallback markup',
    description:
      'Multiplier on the family fallback price of a model that has no ratio of its own. 1.25 means 25% over base cost.',
    positive: true,
    step: '0.01',
  },
  jsonMap(
    'ModelRatio',
    'Model ratio',
    'JSON map of model to input multiplier (quota per token).'
  ),
  jsonMap(
    'CompletionRatio',
    'Completion ratio',
    'JSON map of model to output multiplier relative to the input ratio.'
  ),
  jsonMap(
    'CacheRatio',
    'Prompt cache ratio',
    'JSON map of model to cached-input multiplier relative to the input ratio.'
  ),
  jsonMap(
    'ModelPrice',
    'Model fixed pricing',
    'JSON map of model to USD cost per request. Takes precedence over ratio based billing.'
  ),
  jsonMap(
    'ImageRatio',
    'Image ratio',
    'JSON map of model to image multiplier.'
  ),
  jsonMap(
    'AudioRatio',
    'Audio ratio',
    'JSON map of model to audio input multiplier.'
  ),
  jsonMap(
    'AudioCompletionRatio',
    'Audio completion ratio',
    'JSON map of model to audio output multiplier.'
  ),
  jsonMap(
    'SearchUnitPrice',
    'Search unit price',
    'JSON map of search tool to USD price per unit.'
  ),
  {
    kind: 'json',
    key: 'ContextLengthTiers',
    label: 'Context length tiers',
    expect: 'object',
    description:
      'JSON map of model to ascending context-length tiers, each with a threshold and a ratio. The server checks the tiers when you save.',
    heightClassName: 'h-48 min-h-48 max-h-48',
  },
]

function ResetRatiosButton() {
  const { t } = useTranslation()
  const draft = useOptionDraft()
  const queryClient = useQueryClient()
  const [confirming, setConfirming] = useState(false)
  const [busy, setBusy] = useState(false)

  const ratioDirty =
    JSON.stringify(parseNumberMap(draft.value('ModelRatio'))) !==
    JSON.stringify(parseNumberMap(draft.saved('ModelRatio')))

  const reset = async () => {
    setBusy(true)
    try {
      await resetModelRatios()
      await queryClient.invalidateQueries({
        queryKey: SYSTEM_OPTIONS_QUERY_KEY,
      })
      void queryClient.invalidateQueries({ queryKey: ['status'] })
      toast.success(t('Model ratios were reset to the defaults.'))
    } catch (error) {
      handleServerError(error, t('Failed to reset model ratios'))
    } finally {
      setBusy(false)
      setConfirming(false)
    }
  }

  return (
    <div
      data-settings-form-span='full'
      className='flex flex-wrap items-center justify-between gap-3 rounded-lg border border-dashed px-3 py-2'
    >
      <p className="text-muted-foreground max-w-prose text-sm">
        {t(
          'Reset the model ratio list to the values that ship with this gateway. Fixed prices and the other ratio lists are not touched.'
        )}
      </p>
      <Button
        type='button'
        variant='outline'
        disabled={ratioDirty || busy}
        title={
          ratioDirty ? t('Save or reset your pending changes first.') : undefined
        }
        onClick={() => setConfirming(true)}
      >
        <RotateCcw data-icon='inline-start' />
        {t('Reset model ratios')}
      </Button>
      <ConfirmDialog
        open={confirming}
        onOpenChange={setConfirming}
        title={t('Reset model ratios?')}
        desc={t(
          'Every model ratio you set is replaced by the shipped default. This cannot be undone.'
        )}
        confirmText={t('Reset')}
        destructive
        isLoading={busy}
        handleConfirm={() => void reset()}
      />
    </div>
  )
}

function showField(tab: PricingTab, key: string): boolean {
  if (tab === 'json') return !SCALAR_KEYS.has(key)
  if (tab === 'models') return SCALAR_KEYS.has(key)
  return false
}

export function ModelPricingSection() {
  const { t } = useTranslation()
  const [tab, setTab] = useState<PricingTab>('models')

  return (
    <OptionForm
      fields={PRICING_FIELDS}
      show={(field) => showField(tab, field.key)}
      intro={
        <Tabs
          value={tab}
          onValueChange={(next) => setTab(next as PricingTab)}
        >
          <TabsList>
            <TabsTrigger value='models'>{t('Models')}</TabsTrigger>
            <TabsTrigger value='sync'>{t('Upstream sync')}</TabsTrigger>
            <TabsTrigger value='json'>{t('JSON')}</TabsTrigger>
          </TabsList>
        </Tabs>
      }
    >
      {tab === 'models' ? (
        <>
          <ModelPriceTable />
          <ResetRatiosButton />
        </>
      ) : null}
      {tab === 'sync' ? <UpstreamSync /> : null}
    </OptionForm>
  )
}

const GROUP_FIELDS: OptionField[] = [
  {
    kind: 'json',
    key: 'GroupRatio',
    label: 'Group ratios',
    expect: 'object',
    description:
      'Price multiplier per user group. 1 means list price; the server refuses negative values.',
    validate: validateGroupRatios,
    editor: (props) => <GroupRatioEditor {...props} />,
    heightClassName: 'h-48 min-h-48 max-h-48',
  },
  {
    kind: 'json',
    key: 'GroupGroupRatio',
    label: 'Group overrides',
    expect: 'object',
    description:
      'Per user group, replace the ratio of another group. Structure: {"vip": {"default": 0.9}}.',
    validate: validateGroupGroupRatios,
    editor: (props) => <GroupGroupRatioEditor {...props} />,
    heightClassName: 'h-48 min-h-48 max-h-48',
  },
  {
    kind: 'json',
    key: 'UserUsableGroups',
    label: 'Selectable groups',
    expect: 'object',
    description: 'Groups a person may attach to a key, with the text shown beside each.',
    validate: validateUsableGroups,
    editor: (props) => <UsableGroupsEditor {...props} />,
    heightClassName: 'h-48 min-h-48 max-h-48',
  },
  {
    kind: 'list',
    key: 'AutoGroups',
    label: 'Automatic group order',
    description:
      'One group per line, tried in this order when a key is set to the automatic group.',
    placeholder: 'default',
    rows: 4,
  },
  {
    kind: 'switch',
    key: 'DefaultUseAutoGroup',
    label: 'Use the automatic group by default',
    description: 'New keys start on the automatic group instead of a fixed one',
  },
  {
    kind: 'json',
    key: 'group_ratio_setting.group_special_usable_group',
    label: 'Special usable groups',
    expect: 'object',
    description:
      'Per user group, groups to add ("append_1": "name") or remove ("-:remove_1": "name") from the selectable list.',
    heightClassName: 'h-48 min-h-48 max-h-48',
  },
]

export function GroupPricingSection() {
  return <OptionForm fields={GROUP_FIELDS} />
}
