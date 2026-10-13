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
import { useId, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { CopyButton } from '@/components/copy-button'
import { Dialog } from '@/components/dialog'
import { Button } from '@/components/ui/button'
import { Checkbox } from '@/components/ui/checkbox'
import { Label } from '@/components/ui/label'
import { RadioGroup, RadioGroupItem } from '@/components/ui/radio-group'
import { useQuota } from '@/hooks/use-quota'

import { buildExportContent, type ExportFormat } from '../lib/export'
import type { CreatedBatch } from '../types'
import { useRedemptions } from './redemptions-provider'

interface RedemptionsExportDialogProps {
  batch: CreatedBatch | null
}

/**
 * Shown right after a batch is created: the only moment the full codes are
 * available. Nothing here is stored; closing the dialog drops them.
 */
export function RedemptionsExportDialog({
  batch,
}: RedemptionsExportDialogProps) {
  const { t } = useTranslation()
  const { setDialog } = useRedemptions()
  const { formatQuota } = useQuota()
  const id = useId()
  const [saveToFile, setSaveToFile] = useState(false)
  const [format, setFormat] = useState<ExportFormat>('txt')
  const [includeName, setIncludeName] = useState(true)
  const [includeQuota, setIncludeQuota] = useState(true)

  const keys = batch?.keys ?? []
  const allCodes = keys.join('\n')

  const handleDone = () => {
    if (batch && saveToFile) {
      const content = buildExportContent({
        keys: batch.keys,
        name: batch.name,
        quota: formatQuota(batch.quota),
        format,
        includeName,
        includeQuota,
        labels: { name: t('Name'), code: t('Redemption Code'), quota: t('Quota') },
      })
      const blob = new Blob([content], {
        type:
          format === 'md'
            ? 'text/markdown;charset=utf-8'
            : 'text/plain;charset=utf-8',
      })
      const url = URL.createObjectURL(blob)
      const link = document.createElement('a')
      link.href = url
      link.download = `redemption-codes-${Date.now()}.${format}`
      document.body.append(link)
      link.click()
      link.remove()
      URL.revokeObjectURL(url)
    }
    setSaveToFile(false)
    setDialog(null)
  }

  return (
    <Dialog
      open={batch !== null}
      onOpenChange={(open) => {
        if (!open) handleDone()
      }}
      title={t('Redemption codes created')}
      description={t('Successfully created {{count}} redemption codes', {
        count: keys.length,
      })}
      contentClassName='sm:max-w-lg'
      bodyClassName='space-y-4'
      footer={<Button onClick={handleDone}>{t('Done')}</Button>}
    >
      <p
        role='alert'
        className='border-warning/40 bg-warning/10 rounded-md border px-3 py-2 text-sm'
      >
        {t(
          'These codes are shown only once. Copy or save them now; afterwards the list only shows them masked.'
        )}
      </p>

      <div className='space-y-2'>
        <div className='flex items-center justify-between gap-2'>
          <Label htmlFor={`${id}-codes`}>{t('Redemption Code')}</Label>
          <CopyButton
            value={allCodes}
            variant='outline'
            size='icon'
            tooltip={t('Copy all codes')}
            successTooltip={t('Codes copied!')}
            aria-label={t('Copy all codes')}
          />
        </div>
        <textarea
          id={`${id}-codes`}
          readOnly
          rows={Math.min(Math.max(keys.length, 2), 8)}
          value={allCodes}
          onFocus={(event) => event.currentTarget.select()}
          className='border-input bg-background w-full rounded-md border px-3 py-2 font-mono text-xs'
        />
      </div>

      <div className='flex items-center gap-2'>
        <Checkbox
          id={`${id}-save-file`}
          checked={saveToFile}
          onCheckedChange={setSaveToFile}
        />
        <Label htmlFor={`${id}-save-file`}>{t('Save as a file')}</Label>
      </div>
      {saveToFile && (
        <div className='space-y-4 pl-6'>
          <RadioGroup
            value={format}
            onValueChange={(value) => {
              if (value === 'txt' || value === 'md') setFormat(value)
            }}
            aria-label={t('Save redemption codes')}
            className='flex flex-wrap gap-x-5 gap-y-3'
          >
            {[
              { value: 'txt', label: t('Save as TXT') },
              { value: 'md', label: t('Save as Markdown') },
            ].map((option) => (
              <div key={option.value} className='flex items-center gap-2'>
                <RadioGroupItem
                  value={option.value}
                  id={`${id}-${option.value}`}
                />
                <Label htmlFor={`${id}-${option.value}`}>{option.label}</Label>
              </div>
            ))}
          </RadioGroup>
          <div className='flex flex-wrap gap-6'>
            <div className='flex items-center gap-2'>
              <Checkbox
                id={`${id}-name`}
                checked={includeName}
                onCheckedChange={setIncludeName}
              />
              <Label htmlFor={`${id}-name`}>{t('Include name')}</Label>
            </div>
            <div className='flex items-center gap-2'>
              <Checkbox
                id={`${id}-quota`}
                checked={includeQuota}
                onCheckedChange={setIncludeQuota}
              />
              <Label htmlFor={`${id}-quota`}>{t('Include quota')}</Label>
            </div>
          </div>
        </div>
      )}
    </Dialog>
  )
}
