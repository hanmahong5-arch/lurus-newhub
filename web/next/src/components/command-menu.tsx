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
import { useNavigate } from '@tanstack/react-router'
import { Laptop, Moon, Sun } from 'lucide-react'
import { useCallback, useMemo } from 'react'
import { useTranslation } from 'react-i18next'

import {
  Command,
  CommandDialog,
  CommandEmpty,
  CommandGroup,
  CommandInput,
  CommandItem,
  CommandList,
  CommandSeparator,
} from '@/components/ui/command'
import { ScrollArea } from '@/components/ui/scroll-area'
import { useSearch } from '@/context/search-provider'
import { useTheme } from '@/context/theme-provider'
import { currentUserQueryOptions, userAccess } from '@/lib/user'

import { visibleNavGroups } from './layout/nav'

/**
 * Ctrl/Cmd+K command palette. It lists exactly what the sidebar lists for the
 * caller (access gating included) and the theme
 * actions.
 */
export function CommandMenu() {
  const { t } = useTranslation()
  const navigate = useNavigate()
  const { setTheme } = useTheme()
  const { open, setOpen } = useSearch()
  const { data: user } = useQuery(currentUserQueryOptions)

  const navGroups = useMemo(() => visibleNavGroups(userAccess(user)), [user])

  const runCommand = useCallback(
    (command: () => unknown) => {
      setOpen(false)
      command()
    },
    [setOpen]
  )

  return (
    <CommandDialog
      modal
      open={open}
      onOpenChange={setOpen}
      title={t('Command palette')}
      description={t('Search pages and actions')}
    >
      <Command>
        <CommandInput placeholder={t('Type a command or search...')} />
        <CommandList>
          <ScrollArea className='h-72 pe-1'>
            <CommandEmpty>{t('No results found.')}</CommandEmpty>
            {navGroups.map((group) => (
              <CommandGroup key={group.id} heading={t(group.label)}>
                {group.items.map((item) => (
                  <CommandItem
                    key={item.id}
                    value={`${t(group.label)} ${t(item.label)} ${item.to}`}
                    onSelect={() =>
                      runCommand(() => navigate({ to: item.to }))
                    }
                  >
                    <item.icon className='text-muted-foreground size-4' />
                    {t(item.label)}
                  </CommandItem>
                ))}
              </CommandGroup>
            ))}
            <CommandSeparator />
            <CommandGroup heading={t('Theme')}>
              <CommandItem onSelect={() => runCommand(() => setTheme('light'))}>
                <Sun /> <span>{t('Light')}</span>
              </CommandItem>
              <CommandItem onSelect={() => runCommand(() => setTheme('dark'))}>
                <Moon className='scale-90' />
                <span>{t('Dark')}</span>
              </CommandItem>
              <CommandItem
                onSelect={() => runCommand(() => setTheme('system'))}
              >
                <Laptop />
                <span>{t('System')}</span>
              </CommandItem>
            </CommandGroup>
          </ScrollArea>
        </CommandList>
      </Command>
    </CommandDialog>
  )
}
