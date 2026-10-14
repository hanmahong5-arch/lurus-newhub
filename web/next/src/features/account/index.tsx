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
import { Bell, ShieldCheck, UserRound } from 'lucide-react'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'

import { ErrorState } from '@/components/error-state'
import { SectionPageLayout } from '@/components/section-page-layout'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { toApiError } from '@/lib/api'

import { accountKeys, fetchProfile } from './api'
import { NotificationsTab } from './components/notifications-tab'
import {
  IdentityCard,
  LanguageCard,
  ProfileHeader,
} from './components/profile-tab'
import { SecurityTab } from './components/security-tab'

type TabKey = 'profile' | 'notifications' | 'security'

export function AccountPage() {
  const { t, i18n } = useTranslation()
  const [tab, setTab] = useState<TabKey>('profile')

  const profile = useQuery({
    queryKey: accountKeys.profile,
    queryFn: fetchProfile,
    retry: false,
  })

  return (
    <SectionPageLayout>
      <SectionPageLayout.Title>
        {t('Account & Security')}
      </SectionPageLayout.Title>
      <SectionPageLayout.Content>
        <div className='mx-auto w-full max-w-7xl'>
          <Tabs value={tab} onValueChange={(value) => setTab(value as TabKey)}>
            <TabsList
              variant='line'
              className='mb-3 h-auto w-full justify-start overflow-x-auto sm:mb-4'
            >
              <TabsTrigger value='profile'>
                <UserRound />
                {t('Profile')}
              </TabsTrigger>
              <TabsTrigger value='notifications'>
                <Bell />
                {t('Notifications')}
              </TabsTrigger>
              <TabsTrigger value='security'>
                <ShieldCheck />
                {t('Security')}
              </TabsTrigger>
            </TabsList>

            <TabsContent value='profile' className='space-y-4 sm:space-y-6'>
              {profile.isError ? (
                <ErrorState
                  title={t('Failed to load profile')}
                  description={toApiError(profile.error).message}
                  onRetry={() => void profile.refetch()}
                />
              ) : (
                <>
                  <ProfileHeader
                    profile={profile.data}
                    loading={profile.isPending}
                  />
                  <div className='grid gap-4 sm:gap-5 xl:grid-cols-2 xl:items-start'>
                    <IdentityCard profile={profile.data} />
                    <LanguageCard
                      language={i18n.language}
                      onChange={(code) => void i18n.changeLanguage(code)}
                    />
                  </div>
                </>
              )}
            </TabsContent>

            <TabsContent value='notifications'>
              {profile.isError ? (
                <ErrorState
                  title={t('Failed to load profile')}
                  description={toApiError(profile.error).message}
                  onRetry={() => void profile.refetch()}
                />
              ) : (
                <NotificationsTab profile={profile.data} />
              )}
            </TabsContent>

            <TabsContent value='security'>
              <SecurityTab />
            </TabsContent>
          </Tabs>
        </div>
      </SectionPageLayout.Content>
    </SectionPageLayout>
  )
}
