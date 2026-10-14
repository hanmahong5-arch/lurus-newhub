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
import { ArrowUpDown, Check, Filter } from 'lucide-react'
import { useState, type ReactNode } from 'react'
import { useTranslation } from 'react-i18next'

import { DataTableViewModeToggle } from '@/components/data-table'
import {
  sideDrawerContentClassName,
  sideDrawerFormClassName,
  sideDrawerHeaderClassName,
} from '@/components/drawer-layout'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuGroup,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu'
import {
  Sheet,
  SheetContent,
  SheetDescription,
  SheetHeader,
  SheetTitle,
} from '@/components/ui/sheet'
import { ToggleGroup, ToggleGroupItem } from '@/components/ui/toggle-group'
import { cn } from '@/lib/utils'

import { SORTS, type SortOption, type TokenUnit, type ViewMode } from '../constants'

interface PricingToolbarProps {
  filteredCount: number
  totalCount: number
  sort: SortOption
  onSortChange: (value: SortOption) => void
  unit: TokenUnit
  onUnitChange: (value: TokenUnit) => void
  view: ViewMode
  onViewChange: (value: ViewMode) => void
  activeFilterCount: number
  /** The sidebar, rendered inside the mobile filter sheet. */
  filters: ReactNode
}

function useSortLabels(): Record<SortOption, string> {
  const { t } = useTranslation()
  return {
    name: t('Name'),
    'price-low': t('Price: Low to High'),
    'price-high': t('Price: High to Low'),
  }
}

export function PricingToolbar(props: PricingToolbarProps) {
  const { t } = useTranslation()
  const [filtersOpen, setFiltersOpen] = useState(false)
  const labels = useSortLabels()

  return (
    <div className='bg-card rounded-xl border p-3'>
      <div className='flex flex-wrap items-center justify-between gap-3'>
        <div className='flex items-center gap-2'>
          <Button
            type='button'
            variant='outline'
            size='sm'
            onClick={() => setFiltersOpen(true)}
            className='gap-1.5 xl:hidden'
          >
            <Filter className='size-4' />
            {t('Filter')}
            {props.activeFilterCount > 0 && (
              <Badge className='ms-0.5 size-5 justify-center p-0 text-[10px]'>
                {props.activeFilterCount}
              </Badge>
            )}
          </Button>

          <p
            className='text-muted-foreground flex items-baseline gap-1 text-sm'
            aria-live='polite'
          >
            <span className='text-foreground font-semibold tabular-nums'>
              {props.filteredCount.toLocaleString()}
            </span>
            <span>{props.filteredCount === 1 ? t('model') : t('models')}</span>
            {props.filteredCount !== props.totalCount && (
              <span className='text-muted-foreground/60 text-xs'>
                / {props.totalCount.toLocaleString()}
              </span>
            )}
          </p>
        </div>

        <div className='flex min-w-0 flex-wrap items-center gap-2'>
          <ToggleGroup
            value={[props.unit]}
            onValueChange={(values) => {
              if (values[0] === 'M' || values[0] === 'K') {
                props.onUnitChange(values[0])
              }
            }}
            variant='outline'
            size='sm'
            aria-label={t('Token unit')}
          >
            <ToggleGroupItem value='M'>/1M</ToggleGroupItem>
            <ToggleGroupItem value='K'>/1K</ToggleGroupItem>
          </ToggleGroup>

          <DropdownMenu modal={false}>
            <DropdownMenuTrigger
              render={
                <Button
                  type='button'
                  variant='outline'
                  size='sm'
                  className='h-8 gap-1.5 px-3 text-xs'
                  aria-label={t('Sort')}
                />
              }
            >
              <ArrowUpDown className='size-3.5' />
              <span>{labels[props.sort]}</span>
            </DropdownMenuTrigger>
            <DropdownMenuContent align='end' className='w-48'>
              <DropdownMenuGroup>
                {SORTS.map((value) => (
                  <DropdownMenuItem
                    key={value}
                    onClick={() => props.onSortChange(value)}
                    className='gap-2'
                  >
                    <Check
                      className={cn(
                        'size-4 shrink-0',
                        props.sort === value ? 'opacity-100' : 'opacity-0'
                      )}
                    />
                    {labels[value]}
                  </DropdownMenuItem>
                ))}
              </DropdownMenuGroup>
            </DropdownMenuContent>
          </DropdownMenu>

          <DataTableViewModeToggle
            value={props.view}
            onChange={props.onViewChange}
          />
        </div>
      </div>

      <Sheet open={filtersOpen} onOpenChange={setFiltersOpen}>
        <SheetContent
          side='left'
          className={sideDrawerContentClassName('sm:max-w-md')}
        >
          <SheetHeader className={sideDrawerHeaderClassName()}>
            <SheetTitle>{t('Filter')}</SheetTitle>
            <SheetDescription>
              {t('Filter models by vendor, group, type, endpoint and tags.')}
            </SheetDescription>
          </SheetHeader>
          <div className={sideDrawerFormClassName('gap-0')}>{props.filters}</div>
        </SheetContent>
      </Sheet>
    </div>
  )
}
