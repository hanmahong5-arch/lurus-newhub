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
import { Layers } from 'lucide-react'
import { useTranslation } from 'react-i18next'

import { Badge } from '@/components/ui/badge'
import { TitledCard } from '@/components/ui/titled-card'

import type { Pool } from '../types'
import { ChannelSection } from './channel-section'

export function PoolCard(props: { pool: Pool }) {
  const { t } = useTranslation()
  const { pool } = props
  return (
    <TitledCard
      title={t('Pool · {{group}}', { group: pool.group })}
      icon={<Layers className='size-4' />}
      action={
        <Badge variant='secondary'>
          {t('{{count}} channels', { count: pool.channels.length })}
        </Badge>
      }
      contentClassName='grid gap-3'
    >
      {pool.channels.map((channel) => (
        <ChannelSection key={channel.id} channel={channel} />
      ))}
    </TitledCard>
  )
}
