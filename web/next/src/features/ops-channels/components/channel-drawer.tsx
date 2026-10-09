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
import type { ChannelListItem } from '../types'
import { HealthTab } from './health-tab'
import { KeysTab } from './keys-tab'
import { PlanTab } from './plan-tab'
import { UsageTab } from './usage-tab'

export function ChannelDrawer(props: {
  channel: ChannelListItem | null
  canWrite: boolean
  onClose: () => void
}) {
  const ch = props.channel
  return (
    <Sheet
      open={ch !== null}
      onOpenChange={(open) => {
        if (!open) props.onClose()
      }}
    >
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
                channel={ch}
                canWrite={props.canWrite}
              />
            </div>
          </>
        )}
      </SheetContent>
    </Sheet>
  )
}

function DrawerBody(props: { channel: ChannelListItem; canWrite: boolean }) {
  const { t } = useTranslation()
  const id = props.channel.id
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
