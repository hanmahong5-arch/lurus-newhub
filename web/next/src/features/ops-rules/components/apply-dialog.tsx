import { useQuery } from '@tanstack/react-query'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'

import { Button } from '@/components/ui/button'
import { Checkbox } from '@/components/ui/checkbox'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { toApiError } from '@/lib/api'

import { MAX_APPLY_CHANNELS, channelsQueryOptions } from '../api'
import { parseChannelIds } from '../lib/json'
import type { ChannelTemplate } from '../types'
import { Field } from './field'

interface ApplyDialogProps {
  template: ChannelTemplate
  applying: boolean
  onApply: (channelIds: number[]) => void
  onClose: () => void
}

export function ApplyDialog(props: ApplyDialogProps) {
  const { t } = useTranslation()
  const { template } = props
  const [page] = useState(1)
  const [picked, setPicked] = useState<Set<number>>(new Set())
  const [extra, setExtra] = useState('')
  const [error, setError] = useState<string | null>(null)

  const channels = useQuery(channelsQueryOptions(page))
  const options = channels.data?.channels ?? []

  const toggle = (id: number, on: boolean) => {
    setError(null)
    setPicked((s) => {
      const next = new Set(s)
      if (on) next.add(id)
      else next.delete(id)
      return next
    })
  }

  const submit = () => {
    if (props.applying) return
    let extraIds: number[] = []
    if (extra.trim() !== '') {
      const parsed = parseChannelIds(extra)
      if (parsed === null) {
        setError(t('Enter channel ids as whole numbers separated by commas.'))
        return
      }
      extraIds = parsed
    }
    const ids = [...new Set([...picked, ...extraIds])]
    if (ids.length === 0) {
      setError(t('Choose at least one channel.'))
      return
    }
    if (ids.length > MAX_APPLY_CHANNELS) {
      setError(
        t('At most {{count}} channels can be updated at once.', {
          count: MAX_APPLY_CHANNELS,
        })
      )
      return
    }
    props.onApply(ids)
  }

  let channelList
  if (channels.isPending) {
    channelList = (
      <p className='text-muted-foreground text-sm'>{t('Loading...')}</p>
    )
  } else if (channels.isError) {
    channelList = (
      <p role='alert' className='text-destructive text-sm'>
        {t('Could not load the channel list: {{message}}', {
          message: toApiError(channels.error).message,
        })}
      </p>
    )
  } else if (options.length === 0) {
    channelList = (
      <p className='text-muted-foreground text-sm'>
        {t('No channels to list. Enter ids below.')}
      </p>
    )
  } else {
    channelList = (
      <div
        className='max-h-56 overflow-y-auto rounded-md border p-2'
        data-testid='apply-channel-list'
      >
        {options.map((c) => (
          <label
            key={c.id}
            className='flex cursor-pointer items-center gap-2 py-1 text-sm'
          >
            <Checkbox
              checked={picked.has(c.id)}
              onCheckedChange={(on) => toggle(c.id, on === true)}
              aria-label={`#${c.id} ${c.name}`}
            />
            <span className='text-muted-foreground'>#{c.id}</span>
            <span className='truncate'>{c.name}</span>
          </label>
        ))}
      </div>
    )
  }

  return (
    <Dialog
      open
      onOpenChange={(open) => {
        if (!open && !props.applying) props.onClose()
      }}
    >
      <DialogContent className='sm:max-w-lg'>
        <DialogHeader>
          <DialogTitle>
            {t('Apply "{{name}}" (version {{version}})', {
              name: template.name,
              version: template.version,
            })}
          </DialogTitle>
          <DialogDescription>
            {t(
              'The overrides in the template are written into each chosen channel and replace its current values for the fields the template sets.'
            )}
          </DialogDescription>
        </DialogHeader>

        <div className='grid gap-4'>
          <div className='grid gap-1.5'>
            <p className='text-sm font-medium'>{t('Channels')}</p>
            {channelList}
            {channels.data != null && channels.data.total > options.length && (
              <p className='text-muted-foreground text-xs'>
                {t(
                  'Showing the first {{shown}} of {{total}} channels. Add the others by id below.',
                  { shown: options.length, total: channels.data.total }
                )}
              </p>
            )}
          </div>

          <Field
            label={t('More channel ids')}
            hint={t('Optional, separated by commas.')}
          >
            <Input
              value={extra}
              onChange={(e) => {
                setExtra(e.target.value)
                setError(null)
              }}
              placeholder='12, 15, 21'
              aria-label={t('More channel ids')}
            />
          </Field>

          {error != null && (
            <p role='alert' className='text-destructive text-sm'>
              {error}
            </p>
          )}
        </div>

        <DialogFooter>
          <Button
            type='button'
            variant='outline'
            onClick={props.onClose}
            disabled={props.applying}
          >
            {t('Cancel')}
          </Button>
          <Button type='button' onClick={submit} disabled={props.applying}>
            {t('Apply')}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
