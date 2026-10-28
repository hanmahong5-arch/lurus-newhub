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
import { cn } from '@/lib/utils'

import {
  ACTIVITY_DAY_PRESETS,
  type ActivityDays,
} from '../lib/search'

const LABELS: Record<ActivityDays, string> = {
  1: '1 Day',
  7: '7 Days',
  29: '29 Days',
}

export function PeriodControl(props: {
  days: ActivityDays
  onChange: (days: ActivityDays) => void
  disabled?: boolean
}) {
  const { t } = useTranslation()
  return (
    <div
      role='group'
      aria-label={t('Period')}
      className='flex flex-wrap items-center gap-1'
      data-testid='activity-period'
    >
      {ACTIVITY_DAY_PRESETS.map((d) => (
        <Button
          key={d}
          type='button'
          size='sm'
          variant={props.days === d ? 'default' : 'outline'}
          disabled={props.disabled}
          className={cn(props.days === d && 'pointer-events-none')}
          onClick={() => props.onChange(d)}
        >
          {t(LABELS[d])}
        </Button>
      ))}
    </div>
  )
}
