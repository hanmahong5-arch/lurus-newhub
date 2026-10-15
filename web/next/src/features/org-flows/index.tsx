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
import { useState } from 'react'
import { useTranslation } from 'react-i18next'

import { PageHeader } from '@/components/layout/page-header'
import { Tabs, TabsList, TabsTrigger } from '@/components/ui/tabs'

import { ChannelWizard } from './components/channel-wizard'
import { TokenWizard } from './components/token-wizard'

type Flow = 'newChannel' | 'newToken'

/**
 * Guided multi-step setups for tenant admins: connect an upstream vendor
 * channel, or issue a key. Both wizards stay mounted while the other tab is
 * showing, so switching tabs never loses a half-finished flow (a created
 * channel in particular is only reachable through its wizard state).
 */
export function OrgFlowsPage() {
  const { t } = useTranslation()
  const [flow, setFlow] = useState<Flow>('newChannel')

  return (
    <div className='flex min-w-0 flex-col gap-6' data-testid='org-flows'>
      <PageHeader
        title={t('Setup Wizard')}
        description={t(
          'Step-by-step setups: connect a vendor channel, or issue a key.'
        )}
      />
      <Tabs value={flow} onValueChange={(v) => setFlow(v as Flow)}>
        <TabsList variant='line'>
          <TabsTrigger value='newChannel' data-testid='flows-tab-newChannel'>
            {t('New channel')}
          </TabsTrigger>
          <TabsTrigger value='newToken' data-testid='flows-tab-newToken'>
            {t('New key')}
          </TabsTrigger>
        </TabsList>
      </Tabs>
      <div className='max-w-3xl'>
        <div hidden={flow !== 'newChannel'}>
          <ChannelWizard />
        </div>
        <div hidden={flow !== 'newToken'}>
          <TokenWizard active={flow === 'newToken'} />
        </div>
      </div>
    </div>
  )
}
