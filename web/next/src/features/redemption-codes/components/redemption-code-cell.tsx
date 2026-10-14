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

import { MaskedValueDisplay } from '@/components/masked-value-display'
import {
  Tooltip,
  TooltipContent,
  TooltipTrigger,
} from '@/components/ui/tooltip'

import { isMaskedKey } from '../lib/utils'

/**
 * The code of a row. An unused code arrives masked and stays that way: the
 * full value was shown once, when the batch was created. A used or disabled
 * code comes back whole and can be revealed and copied.
 */
export function RedemptionCodeCell({ value }: { value: string }) {
  const { t } = useTranslation()

  if (isMaskedKey(value)) {
    return (
      <Tooltip>
        <TooltipTrigger
          render={
            <span className='text-muted-foreground cursor-help font-mono text-xs' />
          }
        >
          {value}
        </TooltipTrigger>
        <TooltipContent>
          {t('The full code is only shown once, when it is created.')}
        </TooltipContent>
      </Tooltip>
    )
  }

  const masked = `${value.slice(0, 8)}${'*'.repeat(16)}${value.slice(-8)}`
  return (
    <MaskedValueDisplay
      label={t('Full Code')}
      fullValue={value}
      maskedValue={masked}
      copyTooltip={t('Copy redemption code')}
      copyAriaLabel={t('Copy redemption code')}
    />
  )
}
