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
import { useState } from 'react'
import { useTranslation } from 'react-i18next'

import { SectionPageLayout } from '@/components/section-page-layout'

import { getChannels, requireServerSuccess } from './api'
import { ChannelsDialogs } from './components/channels-dialogs'
import { ChannelsPrimaryButtons } from './components/channels-primary-buttons'
import { ChannelsProvider } from './components/channels-provider'
import { ChannelsTable } from './components/channels-table'
import { ImportWizard } from './components/import-wizard'
import { useChannelAccess } from './hooks/use-channel-access'
import { channelsQueryKeys } from './lib'

/**
 * Multi-key channels that bulk-imported keys can be appended to. The list
 * payload already says which channels are multi-key, so this is one request.
 */
function ImportHost(props: { onClose: () => void }) {
  const list = useQuery({
    queryKey: [...channelsQueryKeys.all, 'import-targets'],
    queryFn: async () =>
      requireServerSuccess(await getChannels({ p: 1, page_size: 100 })),
    gcTime: 0,
  })
  const targets = (list.data?.data?.items ?? [])
    .filter((c) => c.channel_info?.is_multi_key === true)
    .map((c) => ({ id: c.id, name: c.name }))
  return <ImportWizard targets={targets} onClose={props.onClose} />
}

export function OpsChannelsPage() {
  const { t } = useTranslation()
  const [importing, setImporting] = useState(false)
  // UI hygiene only: every endpoint re-checks platform staff on the server.
  const { canEditSensitive } = useChannelAccess()

  return (
    <ChannelsProvider>
      <SectionPageLayout fixedContent>
        <SectionPageLayout.Title>
          {t('Channels & Account Pools')}
        </SectionPageLayout.Title>
        <SectionPageLayout.Actions>
          <ChannelsPrimaryButtons
            onImport={canEditSensitive ? () => setImporting(true) : undefined}
          />
        </SectionPageLayout.Actions>
        <SectionPageLayout.Content>
          <ChannelsTable />
        </SectionPageLayout.Content>
      </SectionPageLayout>
      <ChannelsDialogs />
      {importing && <ImportHost onClose={() => setImporting(false)} />}
    </ChannelsProvider>
  )
}
