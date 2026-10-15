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

import { StatusBadge } from '@/components/status-badge'
import { Badge } from '@/components/ui/badge'

import { healthVariant } from '../lib/health'
import type { ModelHealth } from '../types'

export function HealthBadge(props: { health: ModelHealth | null }) {
  const { t } = useTranslation()
  const variant = healthVariant(props.health)

  if (variant === 'never_probed') {
    return (
      <Badge
        variant='outline'
        data-health={variant}
        title={t('Not probed yet — health is unknown, not healthy')}
        className='text-muted-foreground border-dashed'
      >
        {t('never probed')}
      </Badge>
    )
  }
  if (variant === 'auto_disabled') {
    return (
      <StatusBadge
        copyable={false}
        variant='danger'
        data-health={variant}
        label={t('auto-disabled')}
      />
    )
  }
  if (variant === 'failing') {
    return (
      <StatusBadge
        copyable={false}
        variant='warning'
        data-health={variant}
        label={t('failing ({{count}})', {
          count: props.health?.consecutive_failures ?? 0,
        })}
      />
    )
  }
  return (
    <StatusBadge
      copyable={false}
      variant='success'
      data-health={variant}
      label={t('ok')}
    />
  )
}
