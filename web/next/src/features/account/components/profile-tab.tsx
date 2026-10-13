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
import {
  Activity,
  BarChart3,
  Languages,
  UserRound,
  WalletCards,
} from 'lucide-react'
import { useTranslation } from 'react-i18next'

import { StatusBadge } from '@/components/status-badge'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Avatar, AvatarFallback } from '@/components/ui/avatar'
import { Card, CardContent } from '@/components/ui/card'
import { IconBadge, type IconBadgeTone } from '@/components/ui/icon-badge'
import { Label } from '@/components/ui/label'
import { Skeleton } from '@/components/ui/skeleton'
import { TitledCard } from '@/components/ui/titled-card'
import { ToggleGroup, ToggleGroupItem } from '@/components/ui/toggle-group'
import { formatCompactNumber } from '@/components/format'
import { useQuota } from '@/hooks/use-quota'
import { LANGUAGE_OPTIONS, normalizeLanguage } from '@/i18n/languages'
import { getUserAvatarFallback, getUserAvatarStyle } from '@/lib/avatar'
import { getRoleLabel } from '@/lib/roles'

import type { AccountProfile } from '../types'

function displayName(profile: AccountProfile): string {
  return profile.display_name || profile.username
}

export function ProfileHeader(props: {
  profile: AccountProfile | undefined
  loading: boolean
}) {
  const { t } = useTranslation()
  const { formatQuota } = useQuota()

  if (props.loading || !props.profile) {
    return (
      <Card data-card-hover='false' className='gap-0 overflow-hidden py-0'>
        <CardContent
          className='p-4 sm:p-5'
          role='status'
          aria-label={t('Loading...')}
        >
          <div className='flex items-center gap-4'>
            <Skeleton className='size-16 rounded-2xl' />
            <div className='space-y-3'>
              <Skeleton className='h-7 w-48' />
              <Skeleton className='h-4 w-64' />
            </div>
          </div>
        </CardContent>
      </Card>
    )
  }

  const profile = props.profile
  const name = displayName(profile)
  const avatarName = profile.username || name
  const stats: {
    label: string
    value: string
    description: string
    icon: typeof WalletCards
    tone: IconBadgeTone
  }[] = [
    {
      label: t('Current Balance'),
      value: formatQuota(profile.remaining_quota),
      description: t('Remaining quota'),
      icon: WalletCards,
      tone: 'success',
    },
    {
      label: t('Total Usage'),
      value: formatQuota(profile.used_quota),
      description: t('Total consumed quota'),
      icon: BarChart3,
      tone: 'info',
    },
    {
      label: t('API Requests'),
      value: formatCompactNumber(profile.request_count ?? 0),
      description: t('Total requests made'),
      icon: Activity,
      tone: 'chart-4',
    },
  ]

  return (
    <Card data-card-hover='false' className='gap-0 overflow-hidden py-0'>
      <CardContent className='p-3 sm:p-5'>
        <div className='flex items-center gap-3 text-left sm:gap-4'>
          <Avatar className='ring-background h-12 w-12 rounded-xl text-sm ring-2 sm:h-16 sm:w-16 sm:rounded-2xl sm:text-lg sm:ring-4'>
            <AvatarFallback
              className='rounded-xl font-semibold text-white sm:rounded-2xl'
              style={getUserAvatarStyle(avatarName)}
            >
              {getUserAvatarFallback(avatarName)}
            </AvatarFallback>
          </Avatar>
          <div className='min-w-0 flex-1 space-y-1.5 sm:space-y-3'>
            <div className='flex min-w-0 flex-wrap items-center gap-2'>
              <h1 className='truncate text-xl font-semibold tracking-tight sm:text-2xl'>
                {name}
              </h1>
              <StatusBadge
                label={getRoleLabel(profile.role)}
                variant='neutral'
                copyable={false}
              />
              <StatusBadge
                label={`${t('User ID')} ${profile.id}`}
                variant='info'
                copyText={String(profile.id)}
              />
            </div>
            <div className='text-muted-foreground flex flex-wrap items-center gap-x-2 gap-y-0.5 text-xs sm:gap-x-4 sm:text-sm'>
              <span className='truncate'>@{profile.username}</span>
              {profile.email && (
                <>
                  <span aria-hidden>•</span>
                  <span className='truncate'>{profile.email}</span>
                </>
              )}
              {profile.group && (
                <>
                  <span aria-hidden>•</span>
                  <span className='truncate'>{profile.group}</span>
                </>
              )}
            </div>
          </div>
        </div>
      </CardContent>
      <div className='border-t'>
        <div className='divide-border/60 grid grid-cols-3 divide-x'>
          {stats.map((item) => (
            <div key={item.label} className='min-w-0 px-3 py-3 sm:px-5 sm:py-4'>
              <div className='flex items-center gap-2'>
                <IconBadge tone={item.tone} size='stat'>
                  <item.icon />
                </IconBadge>
                <div className='text-muted-foreground truncate text-xs font-medium tracking-wider uppercase'>
                  {item.label}
                </div>
              </div>
              <div className='text-foreground mt-1.5 truncate font-mono text-lg font-bold tracking-tight tabular-nums sm:mt-2 sm:text-2xl'>
                {item.value}
              </div>
              <div className='text-muted-foreground/60 mt-1 hidden text-xs md:block'>
                {item.description}
              </div>
            </div>
          ))}
        </div>
      </div>
    </Card>
  )
}

function ReadOnlyField(props: { label: string; value?: string | number }) {
  const { t } = useTranslation()
  const text =
    props.value === undefined || props.value === ''
      ? t('Not set')
      : String(props.value)
  return (
    <div className='min-w-0 space-y-1'>
      <Label className='text-muted-foreground text-xs'>{props.label}</Label>
      <div className='truncate text-sm font-medium' title={text}>
        {text}
      </div>
    </div>
  )
}

/** Identity is owned by the platform (SSO): shown here, never edited here. */
export function IdentityCard(props: { profile: AccountProfile | undefined }) {
  const { t } = useTranslation()
  const profile = props.profile
  return (
    <TitledCard
      title={t('Account details')}
      description={t('Your sign-in identity')}
      icon={<UserRound className='size-4' />}
      iconTone='primary'
      disableHoverEffect
    >
      <div className='space-y-4'>
        <div className='grid gap-4 sm:grid-cols-2'>
          <ReadOnlyField label={t('Username')} value={profile?.username} />
          <ReadOnlyField
            label={t('Display name')}
            value={profile?.display_name}
          />
          <ReadOnlyField label={t('Email')} value={profile?.email} />
          <ReadOnlyField label={t('Workspace')} value={profile?.tenant_slug} />
          <ReadOnlyField label={t('User group')} value={profile?.group} />
          <ReadOnlyField label={t('API keys')} value={profile?.token_count} />
        </div>
        <Alert>
          <AlertDescription>
            {t(
              'Your name, email and password are managed by the platform account you sign in with. Change them there.'
            )}
          </AlertDescription>
        </Alert>
      </div>
    </TitledCard>
  )
}

/** The console language is a per-browser choice (stored locally). */
export function LanguageCard(props: {
  language: string
  onChange: (code: string) => void
}) {
  const { t } = useTranslation()
  const current = normalizeLanguage(props.language)
  return (
    <TitledCard
      title={t('Language Preferences')}
      description={t('Set the language used across the interface')}
      icon={<Languages className='size-4' />}
      iconTone='chart-4'
      disableHoverEffect
    >
      <div className='flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between sm:gap-4'>
        <div className='space-y-1'>
          <div className='text-sm font-medium'>{t('Interface Language')}</div>
          <p className='text-muted-foreground text-xs sm:text-sm'>
            {t('Saved in this browser and applied immediately.')}
          </p>
        </div>
        <ToggleGroup
          value={[current]}
          onValueChange={(value) => {
            const next = value.find((item) => item !== current)
            if (next) props.onChange(next)
          }}
          aria-label={t('Interface Language')}
          variant='outline'
          spacing={2}
        >
          {LANGUAGE_OPTIONS.map((option) => (
            <ToggleGroupItem key={option.code} value={option.code}>
              {option.label}
            </ToggleGroupItem>
          ))}
        </ToggleGroup>
      </div>
    </TitledCard>
  )
}
