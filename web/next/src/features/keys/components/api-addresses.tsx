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
import { ChevronDown, Globe } from 'lucide-react'
import { useTranslation } from 'react-i18next'

import { CopyButton } from '@/components/copy-button'
import { LoadingState } from '@/components/loading-state'
import { Button } from '@/components/ui/button'
import {
  Item,
  ItemActions,
  ItemContent,
  ItemGroup,
  ItemTitle,
} from '@/components/ui/item'
import {
  Popover,
  PopoverContent,
  PopoverTitle,
  PopoverTrigger,
} from '@/components/ui/popover'
import { useStatus } from '@/hooks/use-status'

import { clientEndpoints, relayHost } from '../lib/snippets'

/** Header popover: the base URLs applications call, each with a copy button. */
export function ApiAddresses() {
  const { t } = useTranslation()
  const { status, loading } = useStatus()
  const host = relayHost(status?.server_address, window.location.origin)

  return (
    <Popover>
      <PopoverTrigger render={<Button variant='outline' size='sm' />}>
        <Globe aria-hidden='true' />
        {t('API addresses')}
        <ChevronDown aria-hidden='true' />
      </PopoverTrigger>
      <PopoverContent
        align='end'
        className='max-h-[min(28rem,var(--available-height))] w-96 max-w-[calc(100vw-2rem)] overflow-y-auto'
      >
        <PopoverTitle>{t('API addresses')}</PopoverTitle>
        {loading ? (
          <LoadingState inline size='sm' message={t('Loading...')} />
        ) : (
          <ItemGroup>
            {clientEndpoints(host).map(([label, url]) => (
              <Item
                key={label}
                variant='muted'
                size='xs'
                className='flex-nowrap items-start'
              >
                <ItemContent className='min-w-0 gap-1'>
                  <ItemTitle className='line-clamp-none break-all'>
                    {t(label)}
                  </ItemTitle>
                  <code className='text-xs break-all select-text'>{url}</code>
                </ItemContent>
                <ItemActions>
                  <CopyButton
                    value={url}
                    size='sm'
                    tooltip={t('Copy base URL')}
                    aria-label={`${t('Copy base URL')}: ${url}`}
                  />
                </ItemActions>
              </Item>
            ))}
          </ItemGroup>
        )}
      </PopoverContent>
    </Popover>
  )
}
