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
import { useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { StatusBadge } from '@/components/status-badge'
import { Badge } from '@/components/ui/badge'
import {
  Command,
  CommandEmpty,
  CommandGroup,
  CommandInput,
  CommandItem,
  CommandList,
} from '@/components/ui/command'

import {
  CHANNEL_PROVIDER_PRESENTATION,
  CHANNEL_TYPE_OPTIONS,
} from '../../constants'
import { ChannelTypeLogo } from '../channel-type-badge'

type ChannelProviderPickerProps = {
  currentType?: number | null
  disabled: boolean
  onSelect: (type: number) => void
}

/** Searchable grid of provider types, shown first when creating a channel. */
export function ChannelProviderPicker(props: ChannelProviderPickerProps) {
  const { t } = useTranslation()
  const [search, setSearch] = useState('')
  const keyword = search.trim().toLocaleLowerCase()

  const options = useMemo(
    () =>
      CHANNEL_TYPE_OPTIONS.map((option) => {
        const presentation = CHANNEL_PROVIDER_PRESENTATION[option.value]
        return {
          id: String(option.value),
          type: option.value,
          label: t(option.label),
          description: presentation ? t(presentation.descriptionKey) : '',
          badge: presentation?.badge,
          searchText: `${option.value} ${option.label} ${t(option.label)}`,
        }
      }).filter((entry) => entry.searchText.toLocaleLowerCase().includes(keyword)),
    [keyword, t]
  )

  const customType = Number(search.trim())
  const canUseCustomType =
    /^\d+$/.test(search.trim()) &&
    Number.isSafeInteger(customType) &&
    customType > 0 &&
    !CHANNEL_TYPE_OPTIONS.some((option) => option.value === customType)
  const currentId =
    props.currentType == null ? undefined : String(props.currentType)

  return (
    <div className='flex min-h-0 flex-1 flex-col gap-4 p-4 sm:p-6'>
      <Command
        label={t('Search providers or type numbers')}
        defaultValue={currentId}
        shouldFilter={false}
        className='min-h-0 flex-1 bg-transparent p-0'
      >
        <CommandInput
          autoFocus
          className='placeholder:text-muted-foreground'
          value={search}
          onValueChange={setSearch}
          placeholder={t('Search providers or type numbers')}
          aria-label={t('Search providers or type numbers')}
        />
        <CommandList className='mt-3 max-h-none min-h-0 flex-1'>
          <CommandEmpty>{t('No matching provider')}</CommandEmpty>
          <CommandGroup className='p-1 [&_[cmdk-group-items]]:grid [&_[cmdk-group-items]]:gap-2.5 md:[&_[cmdk-group-items]]:grid-cols-2 xl:[&_[cmdk-group-items]]:grid-cols-3'>
            {options.map((option) => (
              <CommandItem
                key={option.id}
                value={option.id}
                aria-current={option.id === currentId ? true : undefined}
                data-checked={option.id === currentId}
                aria-label={`${option.label} #${option.type}`}
                disabled={props.disabled}
                onSelect={() => props.onSelect(option.type)}
                className='data-selected:border-primary/50 data-selected:bg-primary/5 min-h-24 flex-col items-stretch justify-between gap-2.5 rounded-lg border p-3 md:min-h-32 [&>svg]:hidden'
              >
                <span className='flex min-w-0 items-start gap-2.5'>
                  <span className='shrink-0'>
                    <ChannelTypeLogo type={option.type} size={20} />
                  </span>
                  <span className='min-w-0 flex-1'>
                    <span className='flex min-w-0 flex-wrap items-center gap-x-2 gap-y-1'>
                      <span
                        className='max-w-full truncate font-medium'
                        title={option.label}
                      >
                        {option.label}
                      </span>
                      {option.badge && (
                        <StatusBadge
                          label={t(option.badge.labelKey)}
                          variant={
                            option.badge.tone === 'warning' ? 'warning' : 'info'
                          }
                          copyable={false}
                          size='sm'
                        />
                      )}
                    </span>
                    {option.description && (
                      <span
                        className='text-muted-foreground mt-1 line-clamp-2 text-xs leading-relaxed break-words'
                        title={option.description}
                      >
                        {option.description}
                      </span>
                    )}
                  </span>
                </span>
                <span className='flex min-w-0 items-center gap-2'>
                  {option.id === currentId && (
                    <Badge className='shrink-0 rounded-md px-1.5 py-0 text-[10px] font-normal'>
                      {t('Current')}
                    </Badge>
                  )}
                  <span className='text-muted-foreground ml-auto shrink-0 text-[11px] tabular-nums'>
                    #{option.type}
                  </span>
                </span>
              </CommandItem>
            ))}
            {canUseCustomType && (
              <CommandItem
                value={`custom:${customType}`}
                aria-label={t('Use channel type {{type}}', {
                  type: customType,
                })}
                disabled={props.disabled}
                onSelect={() => props.onSelect(customType)}
                className='data-selected:border-primary/50 data-selected:bg-primary/5 min-h-24 gap-2.5 rounded-lg border p-3 md:min-h-32 [&>svg]:hidden'
              >
                <span className='shrink-0'>
                  <ChannelTypeLogo type={customType} size={20} />
                </span>
                <span className='min-w-0 break-words'>
                  {t('Use channel type {{type}}', { type: customType })}
                </span>
              </CommandItem>
            )}
          </CommandGroup>
        </CommandList>
      </Command>
    </div>
  )
}
