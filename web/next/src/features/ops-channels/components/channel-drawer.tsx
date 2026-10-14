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
import { useTranslation } from 'react-i18next'

import { ErrorState } from '@/components/error-state'
import { LoadingState } from '@/components/loading-state'
import {
  Sheet,
  SheetContent,
  SheetDescription,
  SheetHeader,
  SheetTitle,
} from '@/components/ui/sheet'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { toApiError } from '@/lib/api'

import { channelDetailQueryOptions } from '../api'
import { channelTypeName } from '../lib/map'
import { useChannelAccess } from '../hooks/use-channel-access'
import { useChannels } from './channels-provider'
import { HealthTab } from './health-tab'
import { KeysTab } from './keys-tab'
import { PlanTab } from './plan-tab'
import { UsageTab } from './usage-tab'

/**
 * Operations sheet for one channel: health, key pool, usage and plan settings
 * (the tenant-scoped v2 console endpoints). Opened from the row menu; the row
 * comes from the table context.
 */
export function ChannelOpsSheet(props: {
  open: boolean
  onOpenChange: (open: boolean) => void
}) {
  const { currentRow } = useChannels()
  const { canEditSensitive } = useChannelAccess()
  const ch = currentRow
  return (
    <Sheet open={props.open && ch !== null} onOpenChange={props.onOpenChange}>
      <SheetContent className='w-full overflow-y-auto sm:max-w-xl'>
        {ch && (
          <>
            <SheetHeader>
              <SheetTitle>{ch.name || `#${ch.id}`}</SheetTitle>
              <SheetDescription>
                {channelTypeName(ch.type)} · #{ch.id}
              </SheetDescription>
            </SheetHeader>
            <div className='px-4 pb-4'>
              <DrawerBody
                key={ch.id}
                channelId={ch.id}
                canWrite={canEditSensitive}
              />
            </div>
          </>
        )}
      </SheetContent>
    </Sheet>
  )
}

function DrawerBody(props: { channelId: number; canWrite: boolean }) {
  const { t } = useTranslation()
  const id = props.channelId
  const detail = useQuery(channelDetailQueryOptions(id))

  let plan
  if (detail.isPending) {
    plan = <LoadingState />
  } else if (detail.isError) {
    plan = (
      <ErrorState
        title={t('Could not load channel settings')}
        description={toApiError(detail.error).message}
        onRetry={() => void detail.refetch()}
      />
    )
  } else {
    plan = (
      <PlanTab
        channelId={id}
        detail={detail.data}
        canWrite={props.canWrite}
      />
    )
  }

  return (
    <Tabs defaultValue='health'>
      <TabsList>
        <TabsTrigger value='health'>{t('Health')}</TabsTrigger>
        <TabsTrigger value='keys'>{t('Keys')}</TabsTrigger>
        <TabsTrigger value='usage'>{t('Usage')}</TabsTrigger>
        <TabsTrigger value='plan'>{t('Plan settings')}</TabsTrigger>
      </TabsList>
      <TabsContent value='health' className='pt-3'>
        <HealthTab channelId={id} detail={detail.data} />
      </TabsContent>
      <TabsContent value='keys' className='pt-3'>
        <KeysTab
          channelId={id}
          detail={detail.data}
          canWrite={props.canWrite}
        />
      </TabsContent>
      <TabsContent value='usage' className='pt-3'>
        <UsageTab channelId={id} />
      </TabsContent>
      <TabsContent value='plan' className='pt-3'>
        {plan}
      </TabsContent>
    </Tabs>
  )
}
