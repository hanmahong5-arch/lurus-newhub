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

import { Button } from '@/components/ui/button'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table'

import { tiersFromDiff, type DiffEntry } from '../lib/pricing'

/** Value of one diff cell: a number, or a tier list for `context_tiers`. */
function DiffValue(props: { entry: DiffEntry; value: unknown }) {
  const { t } = useTranslation()
  if (props.entry.field === 'context_tiers') {
    const tiers = tiersFromDiff(props.value)
    if (tiers.length === 0) {
      return <span className='text-muted-foreground'>{t('No tiers')}</span>
    }
    return (
      <ul className='space-y-0.5'>
        {tiers.map((tier, i) => (
          // oxlint-disable-next-line react/no-array-index-key -- a read-only, never-reordered list
          <li key={i}>
            {t('From {{n}} tokens', { n: tier.threshold_tokens })}
            {': '}
            {[
              tier.model_ratio !== undefined &&
                `${t('Model ratio')} ${tier.model_ratio}`,
              tier.completion_ratio !== undefined &&
                `${t('Completion ratio')} ${tier.completion_ratio}`,
              tier.cache_ratio !== undefined &&
                `${t('Cache ratio')} ${tier.cache_ratio}`,
            ]
              .filter(Boolean)
              .join(', ') || t('flat ratios')}
          </li>
        ))}
      </ul>
    )
  }
  if (typeof props.value === 'number') return <>{String(props.value)}</>
  return <span className='text-muted-foreground'>-</span>
}

/**
 * The dry run of the pending edits. The server computes this against its own
 * baseline, which can differ from what the write applies (a concurrent
 * writer), so the dialog says so instead of promising the exact outcome.
 */
export function PreviewDialog(props: {
  open: boolean
  onOpenChange: (open: boolean) => void
  diffs: DiffEntry[]
  saving: boolean
  canSave: boolean
  onSave: () => void
}) {
  const { t } = useTranslation()
  const fieldLabel = (field: string): string => {
    switch (field) {
      case 'model_ratio':
        return t('Model ratio')
      case 'completion_ratio':
        return t('Completion ratio')
      case 'model_price':
        return t('Model price')
      case 'cache_ratio':
        return t('Cache ratio')
      case 'context_tiers':
        return t('Context tiers')
      default:
        return field
    }
  }

  return (
    <Dialog open={props.open} onOpenChange={props.onOpenChange}>
      <DialogContent className='sm:max-w-3xl' data-testid='pricing-preview-dialog'>
        <DialogHeader>
          <DialogTitle>{t('Preview of price changes')}</DialogTitle>
          <DialogDescription>
            {t('Nothing is saved yet. The result is calculated against the prices on the server and can change if someone else saves first.')}
          </DialogDescription>
        </DialogHeader>
        {props.diffs.length === 0 ? (
          <p className='text-muted-foreground py-6 text-center text-sm'>
            {t('No changes to preview.')}
          </p>
        ) : (
          <div className='max-h-[60vh] overflow-auto'>
            <Table data-testid='pricing-preview-diff'>
              <TableHeader>
                <TableRow>
                  <TableHead>{t('Model name')}</TableHead>
                  <TableHead>{t('Field')}</TableHead>
                  <TableHead>{t('Before')}</TableHead>
                  <TableHead>{t('After')}</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {props.diffs.map((d) => (
                  <TableRow key={`${d.model_name}-${d.field}`}>
                    <TableCell className='font-mono text-xs'>
                      {d.model_name}
                    </TableCell>
                    <TableCell title={d.field}>{fieldLabel(d.field)}</TableCell>
                    <TableCell className='text-muted-foreground font-mono text-xs whitespace-normal'>
                      <DiffValue entry={d} value={d.old} />
                      {d.old_explicit === false && d.field !== 'context_tiers' && (
                        <span className='ms-1 font-sans'>
                          ({t('default value')})
                        </span>
                      )}
                    </TableCell>
                    <TableCell className='font-mono text-xs whitespace-normal'>
                      <DiffValue entry={d} value={d.new} />
                    </TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          </div>
        )}
        <DialogFooter>
          <Button
            type='button'
            variant='outline'
            onClick={() => props.onOpenChange(false)}
          >
            {t('Close')}
          </Button>
          {props.canSave && (
            <Button
              type='button'
              onClick={props.onSave}
              disabled={props.saving || props.diffs.length === 0}
              data-testid='pricing-preview-save'
            >
              {props.saving ? t('Saving...') : t('Save changes')}
            </Button>
          )}
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
