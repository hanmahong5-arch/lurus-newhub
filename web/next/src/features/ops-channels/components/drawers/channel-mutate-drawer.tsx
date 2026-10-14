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
import { zodResolver } from '@hookform/resolvers/zod'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { ArrowLeft, Loader2, Server } from 'lucide-react'
import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { useForm } from 'react-hook-form'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { ConfirmDialog } from '@/components/confirm-dialog'
import {
  sideDrawerContentClassName,
  sideDrawerFooterClassName,
  sideDrawerFormClassName,
  sideDrawerHeaderClassName,
} from '@/components/drawer-layout'
import { ErrorState } from '@/components/error-state'
import { LoadingState } from '@/components/loading-state'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { Form } from '@/components/ui/form'
import { IconBadge } from '@/components/ui/icon-badge'
import {
  Sheet,
  SheetClose,
  SheetContent,
  SheetDescription,
  SheetFooter,
  SheetHeader,
  SheetTitle,
} from '@/components/ui/sheet'
import { handleServerError } from '@/lib/handle-server-error'
import { getServerErrorMessage } from '@/lib/server-error-message'

import {
  createChannel,
  fetchModels,
  fetchUpstreamModels,
  getChannel,
  getEnabledModels,
  getGroups,
  getPrefillGroups,
  requireServerSuccess,
  updateChannel,
} from '../../api'
import {
  ERROR_MESSAGES,
  MODEL_FETCHABLE_TYPES,
  SUCCESS_MESSAGES,
} from '../../constants'
import { useChannelKeyDisclosure } from '../../hooks/use-channel-key-disclosure'
import { useChannelAccess } from '../../hooks/use-channel-access'
import {
  CHANNEL_FORM_DEFAULT_VALUES,
  channelFormSchema,
  channelsQueryKeys,
  findMissingModelsInMapping,
  formatModelsArray,
  getChannelTypeLabel,
  mergeModelMappingPairs,
  parseModelsString,
  syncModelsWithMappingPairs,
  transformChannelToFormDefaults,
  transformFormDataToCreatePayload,
  transformFormDataToUpdatePayload,
  type ChannelFormValues,
} from '../../lib'
import type { Channel } from '../../types'
import { ChannelTypeLogo } from '../channel-type-badge'
import { FetchModelsDialog } from '../dialogs/fetch-models-dialog'
import {
  MissingModelsConfirmationDialog,
  type MissingModelsAction,
} from '../dialogs/missing-models-confirmation-dialog'
import { PassthroughWarningDialog } from '../dialogs/passthrough-warning-dialog'
import {
  ModelMappingBatchDialog,
  type ModelMappingBatchResult,
} from '../model-mapping-batch-dialog'
import {
  AdvancedSection,
  AuthSection,
  BasicSection,
  ConnectionSection,
  ModelsSection,
  RoutingSection,
} from './channel-form-fields'
import { ChannelProviderPicker } from './channel-provider-picker'

type ChannelMutateDrawerProps = {
  open: boolean
  onOpenChange: (open: boolean) => void
  currentRow?: Channel | null
}

export function ChannelMutateDrawer(props: ChannelMutateDrawerProps) {
  const { t } = useTranslation()
  const isEditing = Boolean(props.currentRow)
  const channelId = props.currentRow?.id ?? 0
  const dirtyRef = useRef(false)
  const [confirmDiscard, setConfirmDiscard] = useState(false)

  // The list row is enough to show the table, but the editor loads the
  // authoritative record (setting/settings documents included).
  const detail = useQuery({
    queryKey: channelsQueryKeys.detail(channelId),
    queryFn: async () => requireServerSuccess(await getChannel(channelId)),
    enabled: props.open && isEditing,
    gcTime: 0,
  })

  const handleOpenChange = (next: boolean) => {
    if (!next && dirtyRef.current) {
      setConfirmDiscard(true)
      return
    }
    props.onOpenChange(next)
  }

  const channel = detail.data?.data ?? null
  let body
  if (isEditing && detail.isPending) {
    body = (
      <>
        <SheetHeader className={sideDrawerHeaderClassName('pr-12 sm:pr-14')}>
          <SheetTitle>{t('Edit Channel')}</SheetTitle>
          <SheetDescription>{props.currentRow?.name}</SheetDescription>
        </SheetHeader>
        <LoadingState />
      </>
    )
  } else if (isEditing && (detail.isError || !channel)) {
    body = (
      <>
        <SheetHeader className={sideDrawerHeaderClassName('pr-12 sm:pr-14')}>
          <SheetTitle>{t('Edit Channel')}</SheetTitle>
          <SheetDescription>{props.currentRow?.name}</SheetDescription>
        </SheetHeader>
        <ErrorState
          title={t('Could not load channel')}
          description={getServerErrorMessage(detail.error)}
          onRetry={() => void detail.refetch()}
        />
      </>
    )
  } else {
    body = (
      <ChannelEditor
        channel={channel}
        onDirtyChange={(dirty) => {
          dirtyRef.current = dirty
        }}
        onDone={() => {
          dirtyRef.current = false
          props.onOpenChange(false)
        }}
      />
    )
  }

  return (
    <>
      <Sheet open={props.open} onOpenChange={handleOpenChange}>
        <SheetContent
          side='right'
          className={sideDrawerContentClassName('sm:max-w-3xl')}
        >
          {props.open && body}
        </SheetContent>
      </Sheet>
      <ConfirmDialog
        open={confirmDiscard}
        onOpenChange={setConfirmDiscard}
        title={t('Discard changes?')}
        desc={t('Your unsaved changes to this channel will be lost.')}
        confirmText={t('Discard')}
        destructive
        handleConfirm={() => {
          setConfirmDiscard(false)
          dirtyRef.current = false
          props.onOpenChange(false)
        }}
      />
    </>
  )
}

type ChannelEditorProps = {
  /** null = creating a new channel. */
  channel: Channel | null
  onDirtyChange: (dirty: boolean) => void
  onDone: () => void
}

function parsePrefillItems(items: string | string[]): string[] {
  if (Array.isArray(items)) return items
  try {
    const parsed: unknown = JSON.parse(items)
    return Array.isArray(parsed)
      ? parsed.filter((m): m is string => typeof m === 'string')
      : []
  } catch {
    return items
      .split(',')
      .map((m) => m.trim())
      .filter(Boolean)
  }
}

function ChannelEditor({ channel, onDirtyChange, onDone }: ChannelEditorProps) {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const { isRoot } = useChannelAccess()
  const isEditing = channel !== null
  const isMultiKey = channel?.channel_info?.is_multi_key === true

  const form = useForm<ChannelFormValues>({
    resolver: zodResolver(channelFormSchema),
    defaultValues: channel
      ? transformChannelToFormDefaults(channel)
      : CHANNEL_FORM_DEFAULT_VALUES,
  })
  const { isDirty, isSubmitting } = form.formState
  useEffect(() => {
    onDirtyChange(isDirty)
  }, [isDirty, onDirtyChange])

  const [choosingProvider, setChoosingProvider] = useState(!isEditing)
  const [submitError, setSubmitError] = useState<string | null>(null)
  const [fetchOpen, setFetchOpen] = useState(false)
  const [fetchedModels, setFetchedModels] = useState<string[]>([])
  const [batchMappingOpen, setBatchMappingOpen] = useState(false)
  const [passThroughWarningOpen, setPassThroughWarningOpen] = useState(false)
  const [missing, setMissing] = useState<{
    values: ChannelFormValues
    models: string[]
  } | null>(null)

  const type = form.watch('type')
  const modelsValue = form.watch('models')
  const selectedModels = useMemo(
    () => parseModelsString(modelsValue),
    [modelsValue]
  )

  const groups = useQuery({
    queryKey: [...channelsQueryKeys.all, 'groups'],
    queryFn: async () => requireServerSuccess(await getGroups()),
    staleTime: 60_000,
  })
  const catalogue = useQuery({
    queryKey: [...channelsQueryKeys.all, 'models-enabled'],
    queryFn: async () => requireServerSuccess(await getEnabledModels()),
    staleTime: 60_000,
  })
  const prefill = useQuery({
    queryKey: [...channelsQueryKeys.all, 'prefill-groups', 'model'],
    queryFn: async () => requireServerSuccess(await getPrefillGroups('model')),
    staleTime: 60_000,
  })
  const prefillGroups = useMemo(
    () =>
      (prefill.data?.data ?? []).map((g) => ({
        id: g.id,
        name: g.name,
        items: parsePrefillItems(g.items),
      })),
    [prefill.data]
  )

  const keyDisclosure = useChannelKeyDisclosure(true, channel?.id ?? null)

  const fetchUpstream = useCallback(async () => {
    const values = form.getValues()
    const typedKey = values.key.trim().split('\n')[0]?.trim() ?? ''
    const response =
      channel && typedKey === ''
        ? await fetchUpstreamModels(channel.id)
        : await fetchModels({
            base_url: values.base_url?.trim() ?? '',
            type: values.type,
            key: typedKey,
          })
    requireServerSuccess(response, t(ERROR_MESSAGES.FETCH_MODELS_FAILED))
    const list = response.data ?? []
    setFetchedModels(list)
    return list
  }, [channel, form, t])

  const submit = async (values: ChannelFormValues) => {
    setSubmitError(null)
    try {
      if (channel) {
        const payload = transformFormDataToUpdatePayload(values, channel)
        requireServerSuccess(
          await updateChannel(channel.id, payload),
          t(ERROR_MESSAGES.UPDATE_FAILED)
        )
        toast.success(t(SUCCESS_MESSAGES.UPDATED))
      } else {
        requireServerSuccess(
          await createChannel(transformFormDataToCreatePayload(values)),
          t(ERROR_MESSAGES.CREATE_FAILED)
        )
        toast.success(t(SUCCESS_MESSAGES.CREATED))
      }
      await queryClient.invalidateQueries({ queryKey: channelsQueryKeys.all })
      onDone()
    } catch (error) {
      const message = getServerErrorMessage(
        error,
        t(isEditing ? ERROR_MESSAGES.UPDATE_FAILED : ERROR_MESSAGES.CREATE_FAILED)
      )
      setSubmitError(message)
      handleServerError(error, message)
    }
  }

  const onSubmit = async (values: ChannelFormValues) => {
    const models = parseModelsString(values.models)
    const absent = findMissingModelsInMapping(values.model_mapping ?? '', models)
    if (absent.length > 0) {
      setMissing({ values, models: absent })
      return
    }
    await submit(values)
  }

  const handleMissingAction = (action: MissingModelsAction) => {
    const pending = missing
    setMissing(null)
    if (!pending || action === 'cancel') return
    if (action === 'add') {
      void submit({
        ...pending.values,
        models: formatModelsArray([
          ...parseModelsString(pending.values.models),
          ...pending.models,
        ]),
      })
      return
    }
    void submit(pending.values)
  }

  const applyBatchMapping = (result: ModelMappingBatchResult) => {
    const merged = mergeModelMappingPairs(
      form.getValues('model_mapping') ?? '',
      result.pairs
    )
    if (merged === null) {
      toast.error(t('Fix the model redirect JSON before adding rules.'))
      return
    }
    form.setValue('model_mapping', merged, {
      shouldDirty: true,
      shouldValidate: true,
    })
    if (result.syncModels) {
      form.setValue(
        'models',
        syncModelsWithMappingPairs(form.getValues('models'), result.pairs),
        { shouldDirty: true, shouldValidate: true }
      )
    }
  }

  const providerLabel = getChannelTypeLabel(type)
  let description = t('Configure the connection and models, then create the channel.')
  if (choosingProvider) {
    description = t('Choose a provider to configure your channel.')
  } else if (isEditing) {
    description = t("Update channel configuration and click save when you're done.")
  }

  const header = (
    <SheetHeader className={sideDrawerHeaderClassName('pr-12 sm:pr-14')}>
      <div className='flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between'>
        <div className='flex min-w-0 items-center gap-2 sm:gap-3'>
          <SheetTitle className='flex shrink-0 items-center gap-2 sm:gap-3'>
            <IconBadge tone='info' size='title'>
              <Server className='size-5' />
            </IconBadge>
            <span>{isEditing ? t('Edit Channel') : t('Create Channel')}</span>
          </SheetTitle>
          {!choosingProvider && (
            <span
              className='text-muted-foreground flex min-w-0 items-center gap-1.5 text-sm'
              data-testid='channel-provider'
            >
              <ChannelTypeLogo type={type} size={16} />
              <span className='truncate'>{t(providerLabel)}</span>
            </span>
          )}
        </div>
        {!isEditing && (
          <Button
            type='button'
            variant='outline'
            size='sm'
            disabled={isSubmitting}
            onClick={() => setChoosingProvider((v) => !v)}
          >
            {choosingProvider ? (
              <>
                <ArrowLeft data-icon='inline-start' />
                {t('Back to configuration')}
              </>
            ) : (
              t('Change provider')
            )}
          </Button>
        )}
      </div>
      <SheetDescription>{description}</SheetDescription>
    </SheetHeader>
  )

  return (
    <>
      {header}
      {submitError && (
        <Alert variant='destructive' className='mx-4 mt-3 w-auto sm:mx-6'>
          <AlertDescription>{submitError}</AlertDescription>
        </Alert>
      )}
      {choosingProvider && (
        <ChannelProviderPicker
          currentType={isEditing ? type : null}
          disabled={isSubmitting}
          onSelect={(next) => {
            form.setValue('type', next, { shouldDirty: true })
            if (next === 41) form.setValue('vertex_key_type', 'json')
            setChoosingProvider(false)
          }}
        />
      )}
      <Form {...form}>
        <form
          id='channel-form'
          onSubmit={form.handleSubmit(onSubmit)}
          className={sideDrawerFormClassName(choosingProvider ? 'hidden' : '')}
          hidden={choosingProvider}
        >
          <BasicSection
            form={form}
            type={type}
            groupOptions={groups.data?.data ?? []}
          />
          <ConnectionSection form={form} type={type} />
          <AuthSection
            form={form}
            type={type}
            isEditing={isEditing}
            isMultiKey={isMultiKey}
            canReveal={isRoot}
            revealedKey={keyDisclosure.channelKey}
            isRevealing={keyDisclosure.isChannelKeyLoading}
            onReveal={() => void keyDisclosure.handleRevealKey()}
          />
          <ModelsSection
            form={form}
            modelCatalogue={catalogue.data?.data ?? []}
            fetchedModels={fetchedModels}
            prefillGroups={prefillGroups}
            canFetch={MODEL_FETCHABLE_TYPES.has(type)}
            onFetch={() => setFetchOpen(true)}
            onBatchMapping={() => setBatchMappingOpen(true)}
            disabled={isSubmitting}
          />
          <RoutingSection form={form} />
          <AdvancedSection
            form={form}
            type={type}
            onPassThroughChange={(next) => {
              if (!next) return true
              setPassThroughWarningOpen(true)
              return false
            }}
          />
        </form>
      </Form>
      <SheetFooter className={sideDrawerFooterClassName()}>
        <SheetClose
          render={<Button variant='outline' disabled={isSubmitting} />}
        >
          {t('Cancel')}
        </SheetClose>
        {!choosingProvider && (
          <Button form='channel-form' type='submit' disabled={isSubmitting}>
            {isSubmitting && <Loader2 className='mr-2 h-4 w-4 animate-spin' />}
            {isEditing ? t('Update Channel') : t('Create Channel')}
          </Button>
        )}
      </SheetFooter>

      {fetchOpen && (
        <FetchModelsDialog
          open
          onOpenChange={(next) => {
            if (!next) setFetchOpen(false)
          }}
          customFetcher={fetchUpstream}
          existingModelsOverride={selectedModels}
          onModelsSelected={(models) =>
            form.setValue('models', formatModelsArray(models), {
              shouldDirty: true,
              shouldValidate: true,
            })
          }
          channelName={form.getValues('name') || channel?.name}
        />
      )}
      {batchMappingOpen && (
        <ModelMappingBatchDialog
          open
          upstreamModels={fetchedModels}
          channelModels={selectedModels}
          onOpenChange={(next) => {
            if (!next) setBatchMappingOpen(false)
          }}
          onApply={applyBatchMapping}
        />
      )}
      <MissingModelsConfirmationDialog
        open={missing !== null}
        missingModels={missing?.models ?? []}
        onConfirm={handleMissingAction}
      />
      <PassthroughWarningDialog
        open={passThroughWarningOpen}
        onOpenChange={setPassThroughWarningOpen}
        handleConfirm={() => {
          form.setValue('pass_through_body_enabled', true, {
            shouldDirty: true,
          })
          setPassThroughWarningOpen(false)
        }}
      />
      {keyDisclosure.stepUpDialog}
    </>
  )
}
