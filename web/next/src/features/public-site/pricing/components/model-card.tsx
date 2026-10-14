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
import { Link } from '@tanstack/react-router'
import { ChevronRight } from 'lucide-react'
import { memo } from 'react'
import { useTranslation } from 'react-i18next'

import { CopyButton } from '@/components/copy-button'
import { Card, CardContent, CardFooter, CardHeader } from '@/components/ui/card'
import { cn } from '@/lib/utils'

import type { ModelHealth } from '../../api'
import type { PricingSearch, TokenUnit } from '../constants'
import { groupMultiplier, priceView, type PublicModel } from '../lib/model'
import { endpointLabel } from '../lib/labels'
import { HealthBadge, ModelAvatar, PriceSummary, TypeBadge } from './price-cells'
import { LinkButton } from '../../components/link-button'

interface ModelCardProps {
  model: PublicModel
  unit: TokenUnit
  /** The list's URL state: the detail page links back with it. */
  search: PricingSearch
  health?: ModelHealth
}

export const ModelCard = memo(function ModelCard(props: ModelCardProps) {
  const { t } = useTranslation()
  const { model } = props
  const multiplier = groupMultiplier(model, props.search.group)
  const view = priceView(model, multiplier, props.unit)
  const endpoints = model.endpoints.map((e) => endpointLabel(t, e))

  return (
    <Card
      className='hover:ring-foreground/20 h-full min-w-0 gap-3 transition-colors'
      data-testid={`model-card-${model.id}`}
    >
      <CardHeader className='flex flex-row items-start gap-3'>
        <div className='bg-muted/50 flex size-10 shrink-0 items-center justify-center rounded-lg'>
          <ModelAvatar model={model} size={28} />
        </div>
        <div className='min-w-0 flex-1'>
          <h3
            className='line-clamp-2 font-mono text-[15px] leading-snug font-semibold [overflow-wrap:anywhere]'
            title={model.id}
          >
            {model.id}
          </h3>
          {model.vendor && (
            <p
              className='text-muted-foreground mt-1 truncate text-xs'
              title={model.vendor}
            >
              {model.vendor}
            </p>
          )}
        </div>
        <CopyButton
          value={model.id}
          tooltip={t('Copy model name')}
          className='size-7'
          iconClassName='size-3.5'
        />
      </CardHeader>

      <CardContent className='flex flex-1 flex-col gap-3'>
        <div className='flex min-w-0 flex-col gap-1.5'>
          <p className='text-muted-foreground line-clamp-2 text-[13px] leading-5 break-words'>
            {model.description || t('No description available.')}
          </p>
          {model.tags.length > 0 && (
            <div
              role='group'
              aria-label={t('Tags')}
              className='text-muted-foreground flex min-w-0 items-baseline gap-1.5 text-xs'
            >
              <span className='shrink-0'>{t('Tags')}</span>
              <span className='truncate' title={model.tags.join(', ')}>
                {model.tags.slice(0, 2).join(', ')}
              </span>
              {model.tags.length > 2 && (
                <span
                  className='shrink-0'
                  title={model.tags.slice(2).join(', ')}
                >
                  +{model.tags.length - 2}
                </span>
              )}
            </div>
          )}
        </div>

        <div
          role='group'
          aria-label={t('Pricing')}
          className='mt-auto flex min-w-0 flex-col gap-1.5'
        >
          <TypeBadge model={model} className='self-start' />
          <PriceSummary view={view} unit={props.unit} />
        </div>

        {(model.groups.length > 0 || endpoints.length > 0) && (
          <dl
            className={cn(
              'grid min-w-0 gap-3 text-xs',
              model.groups.length > 0 && endpoints.length > 0
                ? 'grid-cols-2'
                : 'grid-cols-1'
            )}
          >
            {model.groups.length > 0 && (
              <div className='flex min-w-0 items-baseline gap-1.5'>
                <dt className='text-muted-foreground shrink-0'>{t('Groups')}</dt>
                <dd className='flex min-w-0 items-baseline gap-1'>
                  <span className='truncate' title={model.groups.join(', ')}>
                    {model.groups[0]}
                  </span>
                  {model.groups.length > 1 && (
                    <span
                      className='text-muted-foreground shrink-0'
                      title={model.groups.slice(1).join(', ')}
                    >
                      +{model.groups.length - 1}
                    </span>
                  )}
                </dd>
              </div>
            )}
            {endpoints.length > 0 && (
              <div className='flex min-w-0 items-baseline gap-1.5'>
                <dt className='text-muted-foreground shrink-0'>
                  {t('Endpoints')}
                </dt>
                <dd className='flex min-w-0 items-baseline gap-1'>
                  <span className='truncate' title={endpoints.join(', ')}>
                    {endpoints.slice(0, 2).join(', ')}
                  </span>
                  {endpoints.length > 2 && (
                    <span
                      className='text-muted-foreground shrink-0'
                      title={endpoints.slice(2).join(', ')}
                    >
                      +{endpoints.length - 2}
                    </span>
                  )}
                </dd>
              </div>
            )}
          </dl>
        )}
      </CardContent>

      <CardFooter className='mt-auto border-0 bg-transparent pt-0'>
        <div className='border-border/60 flex w-full items-center justify-between gap-3 border-t pt-2'>
          <HealthBadge health={props.health} />
          <LinkButton
            variant='ghost'
            size='sm'
            className='ms-auto'
            render={
              <Link
                to='/pricing/$modelId'
                params={{ modelId: model.id }}
                search={props.search}
                aria-label={t('Details of {{model}}', { model: model.id })}
              />
            }
          >
            {t('Details')}
            <ChevronRight aria-hidden className='size-3.5 rtl:rotate-180' />
          </LinkButton>
        </div>
      </CardFooter>
    </Card>
  )
})
