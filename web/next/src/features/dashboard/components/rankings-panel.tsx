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
import { useQuery } from '@tanstack/react-query'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'

import { Button } from '@/components/ui/button'
import {
  Card,
  CardContent,
  CardHeader,
  CardTitle,
} from '@/components/ui/card'
import { Skeleton } from '@/components/ui/skeleton'

import {
  fetchRankings,
  RANKING_DIMENSIONS,
  type RankingDimension,
} from '../lib/rankings'

import { LoadFailed } from './panels'

const DIMENSION_LABEL: Record<RankingDimension, string> = {
  model: 'By model',
  relay_mode: 'By endpoint kind',
  usage_unit: 'By billing unit',
  group: 'By group',
  key: 'By API key',
  user: 'By member',
  product: 'By product',
}

/**
 * Usage share grouped by a selectable dimension. relay_mode splits chat /
 * embeddings / rerank traffic; usage_unit splits token vs search-unit billing.
 */
export function RankingsPanel() {
  const { t } = useTranslation()
  const [by, setBy] = useState<RankingDimension>('model')
  const q = useQuery({
    queryKey: ['dashboard', 'rankings', by],
    queryFn: () => fetchRankings(by),
    retry: false,
  })
  const rows = q.data ?? []
  return (
    <Card data-testid='dashboard-rankings'>
      <CardHeader>
        <CardTitle>{t('Usage breakdown')}</CardTitle>
        <div
          role='radiogroup'
          aria-label={t('Group usage by')}
          className='flex flex-wrap gap-1'
        >
          {RANKING_DIMENSIONS.map((dim) => (
            <Button
              key={dim}
              role='radio'
              aria-checked={by === dim}
              data-testid={`rankings-by-${dim}`}
              variant={by === dim ? 'default' : 'outline'}
              size='sm'
              onClick={() => setBy(dim)}
            >
              {t(DIMENSION_LABEL[dim])}
            </Button>
          ))}
        </div>
      </CardHeader>
      <CardContent>
        {q.isPending && <Skeleton className='h-24 w-full' />}
        {q.isError && (
          <LoadFailed
            message={t('Could not load the usage breakdown.')}
            onRetry={() => void q.refetch()}
          />
        )}
        {q.isSuccess && rows.length === 0 && (
          <div className='text-muted-foreground text-sm'>
            {t('No usage recorded yet.')}
          </div>
        )}
        {q.isSuccess && rows.length > 0 && (
          <ul className='flex flex-col gap-2' data-testid='rankings-rows'>
            {rows.map((r) => (
              <li key={r.name} className='text-sm'>
                <div className='flex justify-between gap-3'>
                  <span className='truncate'>{r.name}</span>
                  <span className='text-muted-foreground tabular-nums'>
                    {r.requests.toLocaleString()} {t('requests')} ·{' '}
                    {r.token_share_pct.toFixed(1)}%
                  </span>
                </div>
                <div className='bg-muted h-1.5 rounded'>
                  <div
                    className='bg-primary h-1.5 rounded'
                    style={{
                      width: `${Math.min(100, Math.max(0, r.token_share_pct))}%`,
                    }}
                  />
                </div>
              </li>
            ))}
          </ul>
        )}
      </CardContent>
    </Card>
  )
}
