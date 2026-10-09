import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useState, type ReactNode } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { ErrorState } from '@/components/error-state'
import { LoadingState } from '@/components/loading-state'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import {
  NativeSelect,
  NativeSelectOption,
} from '@/components/ui/native-select'
import { toApiError } from '@/lib/api'

import {
  TOKEN_PICK_SIZE,
  policyKey,
  retentionQueryOptions,
  saveRetention,
  saveTokenRetention,
  tokenOptionsQueryOptions,
} from '../api'
import {
  RETENTION_ORDER,
  looser,
  type Retention,
  type RetentionView,
} from '../lib/policy'

function useRetentionText() {
  const { t } = useTranslation()
  const name: Record<Retention, string> = {
    '': t('Follow the platform default'),
    full: t('Full'),
    metadata_only: t('Metadata only'),
    none: t('No retention'),
  }
  const detail: Record<Retention, string> = {
    '': t('No setting of your own; the platform default applies.'),
    full: t('Keeps everything: prompts and replies are stored in usage logs together with usage, cost and timing.'),
    metadata_only: t('Keeps usage, cost, timing, model and status. Prompts, replies and other free text are not stored.'),
    none: t('Keeps only the numbers needed for billing and error codes. Prompts, replies, client IP and request fingerprint are not stored.'),
  }
  return { name, detail }
}

function ModeOption(props: {
  mode: Retention
  checked: boolean
  disabled: boolean
  disabledReason: string
  onSelect: () => void
  group: string
}) {
  const { name, detail } = useRetentionText()
  return (
    <label
      className={`flex items-start gap-3 rounded-lg border p-3 ${
        props.disabled ? 'opacity-50' : 'cursor-pointer'
      }`}
      title={props.disabled ? props.disabledReason : undefined}
    >
      <input
        type='radio'
        name={props.group}
        className='mt-1'
        checked={props.checked}
        disabled={props.disabled}
        onChange={props.onSelect}
        aria-label={name[props.mode]}
      />
      <span className='space-y-0.5'>
        <span className='block text-sm font-medium'>{name[props.mode]}</span>
        <span className='text-muted-foreground block text-xs'>
          {detail[props.mode]}
        </span>
      </span>
    </label>
  )
}

function TenantRetention(props: { data: RetentionView }) {
  const { t } = useTranslation()
  const qc = useQueryClient()
  const { name } = useRetentionText()
  const { data } = props
  const [mode, setMode] = useState<Retention>(data.tenant)

  const save = useMutation({
    mutationFn: (m: Retention) => saveRetention(m),
    onSuccess: async () => {
      toast.success(t('Saved'))
      await qc.invalidateQueries({ queryKey: policyKey })
    },
    onError: (e) => toast.error(toApiError(e).message),
  })

  return (
    <div className='space-y-3'>
      <div className='flex flex-wrap items-center gap-2 text-sm'>
        <span className='text-muted-foreground'>{t('Platform default')}</span>
        <Badge variant='outline'>{name[data.platformDefault || 'full']}</Badge>
        <span className='text-muted-foreground'>{t('In effect now')}</span>
        <Badge>{name[data.effective]}</Badge>
      </div>
      <p className='text-muted-foreground text-xs'>
        {t('You can only be stricter than the platform default. The stricter of the platform, organization and key settings always applies.')}
      </p>
      <div className='grid gap-2'>
        <ModeOption
          group='tenant-retention'
          mode=''
          checked={mode === ''}
          disabled={false}
          disabledReason=''
          onSelect={() => setMode('')}
        />
        {RETENTION_ORDER.map((m) => (
          <ModeOption
            key={m}
            group='tenant-retention'
            mode={m}
            checked={mode === m}
            disabled={looser(m, data.platformDefault || 'full')}
            disabledReason={t('Looser than the platform default')}
            onSelect={() => setMode(m)}
          />
        ))}
      </div>
      <Button
        onClick={() => save.mutate(mode)}
        disabled={save.isPending || mode === data.tenant}
      >
        {save.isPending ? t('Saving...') : t('Save')}
      </Button>
    </div>
  )
}

function TokenRetention(props: { floor: Retention }) {
  const { t } = useTranslation()
  const { name } = useRetentionText()
  const tokens = useQuery(tokenOptionsQueryOptions)
  const [tokenId, setTokenId] = useState('')
  const [mode, setMode] = useState<Retention>('')
  const [result, setResult] = useState<string | null>(null)

  const save = useMutation({
    mutationFn: (v: { id: number; mode: Retention }) =>
      saveTokenRetention(v.id, v.mode),
    onSuccess: (r) => {
      toast.success(t('Saved'))
      setResult(
        t('Key #{{id}} now keeps: {{mode}}', {
          id: r.tokenId,
          mode: name[r.effective],
        })
      )
    },
    onError: (e) => toast.error(toApiError(e).message),
  })

  let picker: ReactNode
  if (tokens.isPending) {
    picker = <LoadingState size='sm' inline />
  } else if (tokens.isError) {
    picker = (
      <ErrorState
        className='min-h-[120px]'
        title={t('Failed to load keys')}
        description={tokens.error.message}
        onRetry={() => void tokens.refetch()}
      />
    )
  } else if (tokens.data.items.length === 0) {
    picker = (
      <p className='text-muted-foreground text-sm'>
        {t('There are no keys yet.')}
      </p>
    )
  } else {
    picker = (
      <div className='flex flex-wrap items-end gap-3'>
        <label className='grid gap-1 text-xs'>
          <span className='text-muted-foreground'>{t('Key')}</span>
          <NativeSelect
            value={tokenId}
            onChange={(e) => {
              setTokenId(e.target.value)
              setResult(null)
            }}
            aria-label={t('Key')}
            className='w-64'
          >
            <NativeSelectOption value=''>{t('Select a key')}</NativeSelectOption>
            {tokens.data.items.map((k) => (
              <NativeSelectOption key={k.id} value={String(k.id)}>
                {k.name || `#${k.id}`} ({k.key})
              </NativeSelectOption>
            ))}
          </NativeSelect>
        </label>
        <label className='grid gap-1 text-xs'>
          <span className='text-muted-foreground'>{t('Retention')}</span>
          <NativeSelect
            value={mode}
            onChange={(e) => {
              setMode(e.target.value as Retention)
              setResult(null)
            }}
            aria-label={t('Retention')}
            className='w-56'
          >
            <NativeSelectOption value=''>
              {t('Follow the organization setting')}
            </NativeSelectOption>
            {RETENTION_ORDER.map((m) => (
              <NativeSelectOption
                key={m}
                value={m}
                disabled={looser(m, props.floor)}
              >
                {name[m]}
              </NativeSelectOption>
            ))}
          </NativeSelect>
        </label>
        <Button
          variant='outline'
          disabled={tokenId === '' || save.isPending}
          onClick={() => save.mutate({ id: Number(tokenId), mode })}
        >
          {save.isPending ? t('Saving...') : t('Apply to this key')}
        </Button>
      </div>
    )
  }

  return (
    <div className='space-y-3 rounded-lg border p-4'>
      <div>
        <h3 className='text-sm font-medium'>{t('Retention for a single key')}</h3>
        <p className='text-muted-foreground text-xs'>
          {t('A key can be stricter than the organization, never looser. Its current setting is not shown here; the result of a save is.')}
          {' '}
          {t('Only your own keys are listed; keys of other members are not shown here.')}
        </p>
      </div>
      {picker}
      {tokens.data != null && tokens.data.total > TOKEN_PICK_SIZE && (
        <p className='text-muted-foreground text-xs'>
          {t('Only the first {{n}} keys are listed.', { n: TOKEN_PICK_SIZE })}
        </p>
      )}
      {result !== null && (
        <p role='status' className='text-sm'>
          {result}
        </p>
      )}
    </div>
  )
}

export function RetentionSection() {
  const { t } = useTranslation()
  const q = useQuery(retentionQueryOptions)
  if (q.isPending) return <LoadingState />
  if (q.isError) {
    return (
      <ErrorState
        title={t('Failed to load the retention setting')}
        description={q.error.message}
        onRetry={() => void q.refetch()}
      />
    )
  }
  return (
    <div className='space-y-6'>
      <TenantRetention key={q.data.tenant} data={q.data} />
      <TokenRetention floor={q.data.effective} />
    </div>
  )
}
