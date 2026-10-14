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
import { useNavigate } from '@tanstack/react-router'
import { useState, type ReactNode } from 'react'
import { useTranslation } from 'react-i18next'

import { ErrorState } from '@/components/error-state'
import { SectionPageLayout } from '@/components/section-page-layout'
import { Skeleton } from '@/components/ui/skeleton'
import { ApiError } from '@/lib/api-error'
import { cn } from '@/lib/utils'

import { useOptionMap } from '../hooks/use-system-options'
import {
  DEFAULT_SECTION_ID,
  SECTION_GROUPS,
  findSection,
  type SectionDef,
} from '../sections/registry'
import { SettingsPageProvider } from './settings-page-context'

type SettingsFrameProps = {
  title: ReactNode
  nav: ReactNode
  children: ReactNode
}

/**
 * Title bar with a portal slot for the save / reset buttons (rendered by
 * SettingsPageFormActions) and a status slot for the unsaved-changes badge.
 */
function SettingsFrame({ title, nav, children }: SettingsFrameProps) {
  const [actionsContainer, setActionsContainer] =
    useState<HTMLDivElement | null>(null)
  const [titleStatusContainer, setTitleStatusContainer] =
    useState<HTMLSpanElement | null>(null)

  return (
    <SettingsPageProvider
      actionsContainer={actionsContainer}
      titleStatusContainer={titleStatusContainer}
    >
      <SectionPageLayout>
        <SectionPageLayout.Title>
          <span className='inline-flex max-w-full min-w-0 items-center gap-2 align-middle'>
            <span className='truncate'>{title}</span>
            <span
              ref={setTitleStatusContainer}
              className='inline-flex min-w-0 shrink-0 items-center'
            />
          </span>
        </SectionPageLayout.Title>
        <SectionPageLayout.Actions>
          <div
            ref={setActionsContainer}
            className='flex flex-wrap items-center justify-end gap-2'
          />
        </SectionPageLayout.Actions>
        <SectionPageLayout.Content>
          <div className='grid min-h-0 gap-4 lg:grid-cols-[14rem_minmax(0,1fr)] lg:gap-6'>
            {nav}
            <div className='min-w-0 pb-6'>{children}</div>
          </div>
        </SectionPageLayout.Content>
      </SectionPageLayout>
    </SettingsPageProvider>
  )
}

function SectionNav({
  activeId,
  onSelect,
}: {
  activeId: string
  onSelect: (id: string) => void
}) {
  const { t } = useTranslation()
  return (
    <nav
      aria-label={t('Settings sections')}
      className='flex min-w-0 flex-row gap-1 overflow-x-auto pb-1 lg:sticky lg:top-0 lg:max-h-[calc(100svh-8rem)] lg:flex-col lg:gap-4 lg:self-start lg:overflow-x-visible lg:overflow-y-auto lg:pb-0'
    >
      {SECTION_GROUPS.map((group) => (
        <div
          key={group.id}
          className='flex shrink-0 flex-row items-center gap-1 lg:flex-col lg:items-stretch'
        >
          <div className='text-muted-foreground hidden px-2 text-xs font-medium tracking-wide uppercase lg:block'>
            {t(group.title)}
          </div>
          {group.sections.map((section) => {
            const Icon = section.icon
            const active = section.id === activeId
            return (
              <a
                key={section.id}
                href={`/admin/settings/${section.id}`}
                aria-current={active ? 'page' : undefined}
                onClick={(event) => {
                  if (
                    event.defaultPrevented ||
                    event.button !== 0 ||
                    event.metaKey ||
                    event.ctrlKey ||
                    event.shiftKey ||
                    event.altKey
                  ) {
                    return
                  }
                  event.preventDefault()
                  onSelect(section.id)
                }}
                className={cn(
                  'focus-visible:ring-ring/50 flex shrink-0 items-center gap-2 rounded-md px-2.5 py-1.5 text-sm whitespace-nowrap outline-none focus-visible:ring-3',
                  active
                    ? 'bg-accent text-accent-foreground font-medium'
                    : 'text-muted-foreground hover:bg-accent/50 hover:text-foreground'
                )}
              >
                <Icon className='size-4 shrink-0' aria-hidden />
                <span>{t(section.title)}</span>
              </a>
            )
          })}
        </div>
      ))}
    </nav>
  )
}

function SectionBody({ section }: { section: SectionDef }) {
  const { t } = useTranslation()
  const options = useOptionMap()

  if (options.isLoading) {
    return (
      <div
        className='flex flex-col gap-4'
        role='status'
        aria-label={t('Loading settings...')}
      >
        <Skeleton className='h-9 w-1/2' />
        <Skeleton className='h-24 w-full' />
        <Skeleton className='h-9 w-2/3' />
        <Skeleton className='h-9 w-1/3' />
      </div>
    )
  }

  if (options.isError) {
    const forbidden =
      options.error instanceof ApiError && options.error.status === 403
    return (
      <ErrorState
        title={
          forbidden
            ? t('System settings are limited to the root operator')
            : t('Failed to load settings')
        }
        description={
          forbidden
            ? t(
                'Your account can open this page but the server only lets the root operator read and change platform settings.'
              )
            : undefined
        }
        onRetry={forbidden ? undefined : options.refetch}
      />
    )
  }

  const Body = section.component
  return (
    <div className='flex flex-col gap-4'>
      {section.description ? (
        <p className='text-muted-foreground text-sm'>{t(section.description)}</p>
      ) : null}
      <Body />
    </div>
  )
}

/**
 * System settings: section navigation on the left, the active section on the
 * right. The section slug lives in the URL (/admin/settings/$section) so a
 * section can be linked to and survives a reload.
 */
export function SettingsPage({ section }: { section?: string }) {
  const { t } = useTranslation()
  const navigate = useNavigate()
  const active = findSection(section) ?? findSection(DEFAULT_SECTION_ID)

  if (!active) return null

  return (
    <SettingsFrame
      title={t(active.title)}
      nav={
        <SectionNav
          activeId={active.id}
          onSelect={(id) =>
            void navigate({
              to: '/admin/settings/$section',
              params: { section: id },
            })
          }
        />
      }
    >
      <SectionBody key={active.id} section={active} />
    </SettingsFrame>
  )
}
