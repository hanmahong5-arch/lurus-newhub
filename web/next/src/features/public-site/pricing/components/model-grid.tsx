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
import { ChevronLeft, ChevronRight } from 'lucide-react'
import { useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { Button } from '@/components/ui/button'

import type { ModelHealth } from '../../api'
import { PAGE_SIZE, type PricingSearch, type TokenUnit } from '../constants'
import type { PublicModel } from '../lib/model'
import { ModelCard } from './model-card'

interface ModelGridProps {
  models: PublicModel[]
  unit: TokenUnit
  search: PricingSearch
  health: Map<string, ModelHealth>
}

/** Card view: a responsive grid with previous/next paging. */
export function ModelGrid(props: ModelGridProps) {
  const { t } = useTranslation()
  const [page, setPage] = useState(1)
  const totalPages = Math.max(1, Math.ceil(props.models.length / PAGE_SIZE))
  const current = Math.min(page, totalPages)

  const visible = useMemo(() => {
    const start = (current - 1) * PAGE_SIZE
    return props.models.slice(start, start + PAGE_SIZE)
  }, [current, props.models])

  return (
    <div className='flex flex-col gap-4 sm:gap-5'>
      <div className='grid grid-cols-1 gap-3 sm:gap-4 md:grid-cols-2 xl:grid-cols-3'>
        {visible.map((model) => (
          <ModelCard
            key={model.id}
            model={model}
            unit={props.unit}
            search={props.search}
            health={props.health.get(model.id)}
          />
        ))}
      </div>

      {totalPages > 1 && (
        <nav
          aria-label={t('Page')}
          className='text-muted-foreground flex flex-col items-center justify-between gap-3 border-t px-4 py-3 text-sm sm:flex-row'
        >
          <p>
            {t('Page {{current}} of {{total}}', {
              current,
              total: totalPages,
            })}
          </p>
          <div className='flex items-center gap-2'>
            <Button
              type='button'
              variant='outline'
              size='sm'
              onClick={() => setPage(Math.max(1, current - 1))}
              disabled={current <= 1}
              className='gap-1.5'
            >
              <ChevronLeft className='size-4 rtl:rotate-180' />
              {t('Previous page')}
            </Button>
            <Button
              type='button'
              variant='outline'
              size='sm'
              onClick={() => setPage(Math.min(totalPages, current + 1))}
              disabled={current >= totalPages}
              className='gap-1.5'
            >
              {t('Next page')}
              <ChevronRight className='size-4 rtl:rotate-180' />
            </Button>
          </div>
        </nav>
      )}
    </div>
  )
}
