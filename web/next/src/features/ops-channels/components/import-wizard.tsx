import { useMutation, useQueries, useQueryClient } from '@tanstack/react-query'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { Badge } from '@/components/ui/badge'
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
import { Label } from '@/components/ui/label'
import {
  NativeSelect,
  NativeSelectOption,
} from '@/components/ui/native-select'
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table'
import { Textarea } from '@/components/ui/textarea'
import { toApiError } from '@/lib/api'

import {
  channelDetailQueryOptions,
  channelsQueryKey,
  importChannelKeys,
} from '../api'
import {
  buildImportRequest,
  EMPTY_IMPORT_FORM,
  estimateProbeSeconds,
  type ImportForm,
  type ImportFormError,
} from '../lib/import'
import {
  checkImportInput,
  countImportItems,
  IMPORT_CHANNEL_TYPES,
  IMPORT_CODE_LABELS,
  MAX_IMPORT_ITEMS,
} from '../lib/map'
import type { ChannelListItem, ImportOutcome, ImportRow } from '../types'
import { Field } from './shared'

type Step = 'input' | 'preview' | 'result'

const VARIANTS: Record<string, 'default' | 'secondary' | 'destructive'> = {
  ready: 'default',
  duplicate: 'secondary',
}

function StatusBadge(props: { row: ImportRow }) {
  const { t } = useTranslation()
  const labels: Record<string, string> = {
    ready: t('Ready'),
    duplicate: t('Duplicate'),
    invalid: t('Invalid'),
    probe_failed: t('Test failed'),
  }
  const s = props.row.status
  return (
    <Badge
      variant={VARIANTS[s] ?? 'destructive'}
    >
      {labels[s] ?? s}
    </Badge>
  )
}

function ResultTable(props: { rows: ImportRow[]; committed: boolean }) {
  const { t } = useTranslation()
  return (
    <div className='max-h-72 overflow-auto rounded-lg border'>
      <Table>
        <TableHeader>
          <TableRow>
            <TableHead>#</TableHead>
            <TableHead>{t('Name')}</TableHead>
            <TableHead>{t('Status')}</TableHead>
            <TableHead>{t('Detail')}</TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          {props.rows.map((r) => (
            <TableRow key={r.index} data-testid={`import-row-${r.index}`}>
              <TableCell>{r.index + 1}</TableCell>
              <TableCell className='max-w-32 truncate'>
                {r.name !== '' ? r.name : r.fingerprint.slice(0, 8)}
              </TableCell>
              <TableCell>
                <div className='flex flex-wrap items-center gap-1'>
                  <StatusBadge row={r} />
                  {props.committed && r.imported && (
                    <Badge variant='default'>{t('Imported')}</Badge>
                  )}
                </div>
              </TableCell>
              <TableCell className='text-xs break-all'>
                {r.errorCode !== ''
                  ? t(IMPORT_CODE_LABELS[r.errorCode] ?? r.errorCode)
                  : ''}
                {r.existingChannelId > 0 &&
                  ` (${t('channel #{{id}}', { id: r.existingChannelId })})`}
                {r.probe != null &&
                  (r.probe.ok
                    ? ` ${t('Test passed ({{ms}} ms)', { ms: r.probe.latencyMs })}`
                    : ` ${r.probe.error}`)}
                {props.committed &&
                  r.imported &&
                  ` ${t('channel #{{id}}', { id: r.channelId })}`}
              </TableCell>
            </TableRow>
          ))}
        </TableBody>
      </Table>
    </div>
  )
}

export function ImportWizard(props: {
  /** Channels on the current page; multi-key ones become append targets. */
  channels: ChannelListItem[]
  onClose: () => void
}) {
  const { t } = useTranslation()
  const qc = useQueryClient()
  const [step, setStep] = useState<Step>('input')
  const [form, setForm] = useState<ImportForm>(EMPTY_IMPORT_FORM)
  const [error, setError] = useState<ImportFormError | 'json' | 'too-many' | null>(
    null
  )
  const [preview, setPreview] = useState<ImportOutcome | null>(null)
  const [result, setResult] = useState<ImportOutcome | null>(null)

  const details = useQueries({
    queries: props.channels.map((c) => channelDetailQueryOptions(c.id)),
  })
  const targets = props.channels.filter(
    (_c, i) => details[i]?.data?.isMultiKey === true
  )

  const set = <K extends keyof ImportForm>(k: K, v: ImportForm[K]) => {
    setForm((f) => ({ ...f, [k]: v }))
    setError(null)
  }

  const fail = (e: unknown) => {
    toast.error(toApiError(e).message)
  }

  const dry = useMutation({
    mutationFn: (body: Parameters<typeof importChannelKeys>[0]) =>
      importChannelKeys(body),
    onSuccess: (o) => {
      setPreview(o)
      setStep('preview')
    },
    onError: fail,
  })

  const commit = useMutation({
    mutationFn: (body: Parameters<typeof importChannelKeys>[0]) =>
      importChannelKeys(body),
    onSuccess: async (o) => {
      setResult(o)
      setStep('result')
      await qc.invalidateQueries({ queryKey: channelsQueryKey })
    },
    onError: fail,
  })

  const runPreview = () => {
    const shape = checkImportInput(form.keys)
    if (shape === 'bad-json') return setError('json')
    if (countImportItems(form.keys) > MAX_IMPORT_ITEMS) return setError('too-many')
    const built = buildImportRequest(form, true)
    if (!built.ok) return setError(built.error)
    dry.mutate(built.body)
  }

  const runCommit = () => {
    const built = buildImportRequest(form, false)
    if (!built.ok) return setError(built.error)
    commit.mutate(built.body)
  }

  const errorText = {
    keys: t('Paste at least one key.'),
    type: t('Pick a channel type for the new channels.'),
    json: t('The text starts with [ but is not a valid JSON array.'),
    'too-many': t('At most {{max}} keys per import.', { max: MAX_IMPORT_ITEMS }),
  }

  const ready = preview?.summary.ready ?? 0
  const busy = dry.isPending || commit.isPending

  return (
    <Dialog
      open
      onOpenChange={(open) => {
        if (!open && !busy) props.onClose()
      }}
    >
      <DialogContent className='sm:max-w-2xl'>
        <DialogHeader>
          <DialogTitle>{t('Bulk import keys')}</DialogTitle>
          <DialogDescription>
            {step === 'input' &&
              t('Step 1 of 3: paste keys and choose where they go.')}
            {step === 'preview' &&
              t('Step 2 of 3: review the preview. Nothing is saved yet.')}
            {step === 'result' && t('Step 3 of 3: import finished.')}
          </DialogDescription>
        </DialogHeader>

        {step === 'input' && (
          <div className='grid gap-4'>
            <Field
              label={t('Keys')}
              hint={t(
                'One key per line, or a JSON array of keys / objects (key, name, models, plan_kind, expires_at, proxy, weight, tags). Up to {{max}} per import.',
                { max: MAX_IMPORT_ITEMS }
              )}
            >
              <Textarea
                value={form.keys}
                onChange={(e) => set('keys', e.target.value)}
                rows={6}
                className='font-mono text-xs'
                aria-label={t('Keys')}
              />
            </Field>
            <Field label={t('Destination')}>
              <NativeSelect
                className='w-full'
                value={form.target}
                onChange={(e) => set('target', e.target.value)}
                aria-label={t('Destination')}
              >
                <NativeSelectOption value=''>
                  {t('Create one new channel per key')}
                </NativeSelectOption>
                {targets.map((c) => (
                  <NativeSelectOption key={c.id} value={String(c.id)}>
                    {t('Append to {{name}}', { name: c.name })}
                  </NativeSelectOption>
                ))}
              </NativeSelect>
            </Field>
            {form.target === '' && (
              <div className='grid gap-4 sm:grid-cols-2'>
                <Field label={t('Channel type')}>
                  <NativeSelect
                    className='w-full'
                    value={form.type}
                    onChange={(e) => set('type', e.target.value)}
                    aria-label={t('Channel type')}
                  >
                    <NativeSelectOption value=''>
                      {t('Select a type')}
                    </NativeSelectOption>
                    {IMPORT_CHANNEL_TYPES.map((ty) => (
                      <NativeSelectOption key={ty.id} value={String(ty.id)}>
                        {ty.name}
                      </NativeSelectOption>
                    ))}
                  </NativeSelect>
                </Field>
                <Field label={t('Group')}>
                  <Input
                    value={form.group}
                    onChange={(e) => set('group', e.target.value)}
                    placeholder='default'
                    aria-label={t('Group')}
                  />
                </Field>
                <Field label={t('Base URL')} hint={t('Empty uses the type default.')}>
                  <Input
                    value={form.baseUrl}
                    onChange={(e) => set('baseUrl', e.target.value)}
                    aria-label={t('Base URL')}
                  />
                </Field>
                <Field label={t('Models (comma-separated)')}>
                  <Input
                    value={form.models}
                    onChange={(e) => set('models', e.target.value)}
                    placeholder='model-a,model-b'
                    aria-label={t('Models (comma-separated)')}
                  />
                </Field>
              </div>
            )}
            <Field
              label={t('Proxy URL (optional)')}
              hint={t(
                'Applied to every key. Proxy and base URL go through a safety check; internal addresses are rejected.'
              )}
            >
              <Input
                value={form.proxy}
                onChange={(e) => set('proxy', e.target.value)}
                aria-label={t('Proxy URL (optional)')}
              />
            </Field>
            {error != null && (
              <p role='alert' className='text-destructive text-sm'>
                {errorText[error]}
              </p>
            )}
          </div>
        )}

        {step === 'preview' && preview != null && (
          <div className='grid gap-4'>
            <p className='text-sm' data-testid='import-summary'>
              {t(
                '{{ready}} ready, {{duplicate}} duplicate, {{invalid}} invalid.',
                {
                  ready: preview.summary.ready ?? 0,
                  duplicate: preview.summary.duplicate ?? 0,
                  invalid: preview.summary.invalid ?? 0,
                }
              )}
            </p>
            <ResultTable rows={preview.rows} committed={false} />
            <div className='flex items-start gap-2'>
              <Checkbox
                id='import-probe'
                checked={form.probe}
                onCheckedChange={(v) => set('probe', v === true)}
              />
              <div className='grid gap-1'>
                <Label htmlFor='import-probe'>
                  {t('Test each key before importing')}
                </Label>
                <p className='text-muted-foreground text-xs'>
                  {t(
                    'Tests run one at a time with a random pause between them, so a large batch is slow (about {{seconds}} s for {{count}} keys). Keys that fail the test are skipped.',
                    { seconds: estimateProbeSeconds(ready), count: ready }
                  )}
                </p>
              </div>
            </div>
            {ready === 0 && (
              <p className='text-muted-foreground text-sm'>
                {t('No ready keys to import.')}
              </p>
            )}
          </div>
        )}

        {step === 'result' && result != null && (
          <div className='grid gap-4'>
            <p className='text-sm' data-testid='import-result-summary'>
              {t('{{imported}} imported, {{failed}} test failed, {{skipped}} skipped.', {
                imported: result.summary.imported ?? 0,
                failed: result.summary.probe_failed ?? 0,
                skipped:
                  (result.summary.duplicate ?? 0) + (result.summary.invalid ?? 0),
              })}
            </p>
            <ResultTable rows={result.rows} committed />
          </div>
        )}

        <DialogFooter>
          {step === 'input' && (
            <>
              <Button variant='outline' onClick={props.onClose}>
                {t('Cancel')}
              </Button>
              <Button onClick={runPreview} disabled={dry.isPending}>
                {t('Preview')}
              </Button>
            </>
          )}
          {step === 'preview' && (
            <>
              <Button
                variant='outline'
                onClick={() => setStep('input')}
                disabled={commit.isPending}
              >
                {t('Back')}
              </Button>
              <Button onClick={runCommit} disabled={ready === 0 || commit.isPending}>
                {commit.isPending
                  ? t('Importing...')
                  : t('Import {{count}} keys', { count: ready })}
              </Button>
            </>
          )}
          {step === 'result' && (
            <Button onClick={props.onClose}>{t('Close')}</Button>
          )}
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
