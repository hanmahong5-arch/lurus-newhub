import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Plus } from 'lucide-react'
import { useState, type FormEvent, type ReactNode } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { ConfirmDialog } from '@/components/confirm-dialog'
import { EmptyState } from '@/components/empty-state'
import { ErrorState } from '@/components/error-state'
import { LoadingState } from '@/components/loading-state'
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
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
import { Switch } from '@/components/ui/switch'
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table'
import { toApiError } from '@/lib/api'

import {
  createRule,
  deleteRule,
  policyKey,
  rulesQueryOptions,
  updateRule,
} from '../api'
import {
  PATTERN_TYPES,
  ROLE_SCOPES,
  RULE_KINDS,
  RULE_MODES,
  buildRuleBody,
  emptyRuleForm,
  formFromRule,
  type ContentRule,
  type RuleForm,
  type RuleFormError,
  type RulesView,
} from '../lib/policy'

/** Display names for the detectors the server ships; unknown ones show raw. */
function useBuiltinNames() {
  const { t } = useTranslation()
  const names: Record<string, string> = {
    phone_cn: t('Mainland China mobile number'),
    id_card_cn: t('Mainland China ID card number'),
    bank_card: t('Bank card number'),
    email: t('Email address'),
    secret_key: t('API key or secret shape'),
  }
  return (id: string) => names[id] ?? id
}

function useLabels() {
  const { t } = useTranslation()
  return {
    role: {
      any: t('Any role'),
      system: t('System messages'),
      user: t('User messages'),
      assistant: t('Assistant messages'),
    } as Record<string, string>,
    kind: {
      mask: t('Mask'),
      reject: t('Reject request'),
    } as Record<string, string>,
    mode: {
      observe: t('Observe'),
      enforce: t('Enforce'),
    } as Record<string, string>,
    pattern: {
      builtin: t('Built-in'),
      regex: t('Regular expression'),
    } as Record<string, string>,
  }
}

function Field(props: { label: string; hint?: string; children: ReactNode }) {
  return (
    <div className='grid gap-1.5'>
      <Label>{props.label}</Label>
      {props.children}
      {props.hint != null && (
        <p className='text-muted-foreground text-xs'>{props.hint}</p>
      )}
    </div>
  )
}

function RuleDialog(props: {
  rule?: ContentRule
  view: RulesView
  saving: boolean
  serverError: string | null
  onSubmit: (body: Record<string, unknown>) => void
  onClose: () => void
}) {
  const { t } = useTranslation()
  const labels = useLabels()
  const builtinName = useBuiltinNames()
  const { rule, view } = props
  const [form, setForm] = useState<RuleForm>(() =>
    rule ? formFromRule(rule) : emptyRuleForm(view.builtins)
  )
  const [error, setError] = useState<RuleFormError | null>(null)
  const set = <K extends keyof RuleForm>(k: K, v: RuleForm[K]) => {
    setForm((f) => ({ ...f, [k]: v }))
    setError(null)
  }

  const errorText: Record<RuleFormError, string> = {
    name: t('Enter a name of up to 64 bytes.'),
    builtin: t('Choose what to detect.'),
    pattern: t('Enter a regular expression.'),
    'pattern-long': t('The expression is longer than {{n}} characters.', {
      n: view.maxPatternLen,
    }),
    ordinal: t('Order must be a whole number, 0 or more.'),
  }

  const submit = (e: FormEvent) => {
    e.preventDefault()
    if (props.saving) return
    const built = buildRuleBody(form, view.maxPatternLen)
    if (!built.ok) {
      setError(built.error)
      return
    }
    props.onSubmit(built.body)
  }

  return (
    <Dialog
      open
      onOpenChange={(open) => {
        if (!open && !props.saving) props.onClose()
      }}
    >
      <DialogContent className='sm:max-w-lg'>
        <DialogHeader>
          <DialogTitle>{rule ? t('Edit rule') : t('Add rule')}</DialogTitle>
          <DialogDescription>
            {t('Rules run on request content before it is sent to a model provider.')}
          </DialogDescription>
        </DialogHeader>
        <form onSubmit={submit} className='grid gap-4' noValidate>
          <Field label={t('Name')}>
            <Input
              value={form.name}
              onChange={(e) => set('name', e.target.value)}
              aria-label={t('Name')}
              autoFocus
            />
          </Field>

          <div className='grid gap-4 sm:grid-cols-2'>
            <Field label={t('Applies to')}>
              <NativeSelect
                className='w-full'
                value={form.roleScope}
                onChange={(e) => set('roleScope', e.target.value)}
                aria-label={t('Applies to')}
              >
                {ROLE_SCOPES.map((r) => (
                  <NativeSelectOption key={r} value={r}>
                    {labels.role[r]}
                  </NativeSelectOption>
                ))}
              </NativeSelect>
            </Field>
            <Field label={t('Action on match')}>
              <NativeSelect
                className='w-full'
                value={form.kind}
                onChange={(e) => set('kind', e.target.value)}
                aria-label={t('Action on match')}
              >
                {RULE_KINDS.map((k) => (
                  <NativeSelectOption key={k} value={k}>
                    {labels.kind[k]}
                  </NativeSelectOption>
                ))}
              </NativeSelect>
            </Field>
          </div>

          <Field label={t('Match by')}>
            <NativeSelect
              className='w-full'
              value={form.patternType}
              onChange={(e) => set('patternType', e.target.value)}
              aria-label={t('Match by')}
            >
              {PATTERN_TYPES.map((p) => (
                <NativeSelectOption key={p} value={p}>
                  {labels.pattern[p]}
                </NativeSelectOption>
              ))}
            </NativeSelect>
          </Field>

          {form.patternType === 'builtin' ? (
            <Field label={t('Detect')}>
              <NativeSelect
                className='w-full'
                value={form.builtin}
                onChange={(e) => set('builtin', e.target.value)}
                aria-label={t('Detect')}
              >
                {view.builtins.map((b) => (
                  <NativeSelectOption key={b} value={b}>
                    {builtinName(b)}
                  </NativeSelectOption>
                ))}
              </NativeSelect>
            </Field>
          ) : (
            <Field
              label={t('Regular expression')}
              hint={t('RE2 syntax, at most {{n}} characters.', {
                n: view.maxPatternLen,
              })}
            >
              <Input
                value={form.pattern}
                onChange={(e) => set('pattern', e.target.value)}
                aria-label={t('Regular expression')}
                className='font-mono'
              />
            </Field>
          )}

          {form.kind === 'mask' && (
            <Field
              label={t('Replacement text (optional)')}
              hint={t('Leave empty to use the default placeholder.')}
            >
              <Input
                value={form.replacement}
                onChange={(e) => set('replacement', e.target.value)}
                aria-label={t('Replacement text (optional)')}
              />
            </Field>
          )}

          <div className='grid gap-4 sm:grid-cols-2'>
            <Field label={t('Mode')}>
              <NativeSelect
                className='w-full'
                value={form.mode}
                onChange={(e) => set('mode', e.target.value)}
                aria-label={t('Mode')}
              >
                {RULE_MODES.map((m) => (
                  <NativeSelectOption key={m} value={m}>
                    {labels.mode[m]}
                  </NativeSelectOption>
                ))}
              </NativeSelect>
            </Field>
            <Field label={t('Order')} hint={t('Smaller numbers run first.')}>
              <Input
                inputMode='numeric'
                value={form.ordinal}
                onChange={(e) => set('ordinal', e.target.value)}
                aria-label={t('Order')}
              />
            </Field>
          </div>

          {form.mode === 'observe' ? (
            <p className='text-muted-foreground text-xs'>
              {t('Observe only records matches; requests pass through unchanged.')}
            </p>
          ) : (
            <p role='note' className='text-warning text-xs'>
              {form.kind === 'reject'
                ? t('Enforce will refuse matching requests. Check the observed matches first.')
                : t('Enforce will rewrite matching content before it is sent. Check the observed matches first.')}
            </p>
          )}

          <div className='flex items-center justify-between gap-3'>
            <Label>{t('Enabled')}</Label>
            <Switch
              checked={form.enabled}
              onCheckedChange={(v) => set('enabled', v)}
              aria-label={t('Enabled')}
            />
          </div>

          {(error !== null || props.serverError !== null) && (
            <p role='alert' className='text-destructive text-sm'>
              {error !== null ? errorText[error] : props.serverError}
            </p>
          )}
          <DialogFooter>
            <Button
              type='button'
              variant='outline'
              onClick={props.onClose}
              disabled={props.saving}
            >
              {t('Cancel')}
            </Button>
            <Button type='submit' disabled={props.saving}>
              {props.saving ? t('Saving...') : t('Save')}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}

type Dialogs =
  | { kind: 'create' }
  | { kind: 'edit'; rule: ContentRule }
  | { kind: 'delete'; rule: ContentRule }
  | null

export function RulesSection() {
  const { t } = useTranslation()
  const qc = useQueryClient()
  const labels = useLabels()
  const builtinName = useBuiltinNames()
  const q = useQuery(rulesQueryOptions)
  const [dialog, setDialog] = useState<Dialogs>(null)
  const [serverError, setServerError] = useState<string | null>(null)

  const done = async () => {
    setDialog(null)
    setServerError(null)
    toast.success(t('Saved'))
    await qc.invalidateQueries({ queryKey: [...policyKey, 'rules'] })
  }

  const save = useMutation({
    mutationFn: (v: { id?: number; body: Record<string, unknown> }) =>
      v.id === undefined ? createRule(v.body) : updateRule(v.id, v.body),
    onSuccess: done,
    // Validation messages from the server (bad regex, rule limit) stay in the
    // open dialog next to the field the admin is fixing.
    onError: (e) => setServerError(toApiError(e).message),
  })

  const remove = useMutation({
    mutationFn: (id: number) => deleteRule(id),
    onSuccess: done,
    onError: (e) => {
      setDialog(null)
      toast.error(toApiError(e).message)
    },
  })

  if (q.isPending) return <LoadingState />
  if (q.isError) {
    return (
      <ErrorState
        title={t('Failed to load content rules')}
        description={q.error.message}
        onRetry={() => void q.refetch()}
      />
    )
  }
  const view = q.data
  const atLimit = view.maxRules > 0 && view.rules.length >= view.maxRules
  const observing = view.rules.filter((r) => r.enabled && r.mode === 'observe').length

  const describe = (r: ContentRule) =>
    r.patternType === 'builtin' ? builtinName(r.builtin) : r.pattern

  return (
    <div className='space-y-4'>
      <Alert>
        <AlertTitle>{t('Observe first, then enforce')}</AlertTitle>
        <AlertDescription>
          <ol className='list-decimal space-y-0.5 ps-4'>
            <li>{t('Add the rule in Observe mode. Matches are recorded and requests pass through unchanged.')}</li>
            <li>{t('Review the matches for a few days in the audit log tab (action data_policy.rule_hit).')}</li>
            <li>{t('When the matches are what you expect, edit the rule and switch it to Enforce.')}</li>
          </ol>
        </AlertDescription>
      </Alert>

      <div className='flex flex-wrap items-center justify-between gap-2'>
        <p className='text-muted-foreground text-sm'>
          {t('{{total}} rules, {{observing}} still observing.', {
            total: view.rules.length,
            observing,
          })}
          {view.maxRules > 0 && ` ${t('Limit: {{n}}.', { n: view.maxRules })}`}
        </p>
        <Button
          onClick={() => {
            setServerError(null)
            setDialog({ kind: 'create' })
          }}
          disabled={atLimit}
          title={atLimit ? t('Rule limit reached') : undefined}
        >
          <Plus />
          {t('Add rule')}
        </Button>
      </div>

      {view.rules.length === 0 ? (
        <EmptyState
          title={t('No content rules yet')}
          description={t('Rules can mask or refuse phone numbers, ID cards, bank cards, emails and key-shaped text.')}
          bordered
        />
      ) : (
        <div className='overflow-x-auto rounded-lg border'>
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>{t('Order')}</TableHead>
                <TableHead>{t('Name')}</TableHead>
                <TableHead>{t('Matches')}</TableHead>
                <TableHead>{t('Applies to')}</TableHead>
                <TableHead>{t('Action on match')}</TableHead>
                <TableHead>{t('Mode')}</TableHead>
                <TableHead>{t('Status')}</TableHead>
                <TableHead className='text-right'>{t('Actions')}</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {view.rules.map((r) => (
                <TableRow key={r.id} data-testid='rule-row'>
                  <TableCell>{r.ordinal}</TableCell>
                  <TableCell className='font-medium'>{r.name}</TableCell>
                  <TableCell className='max-w-56 truncate font-mono text-xs'>
                    {describe(r)}
                  </TableCell>
                  <TableCell>{labels.role[r.roleScope] ?? r.roleScope}</TableCell>
                  <TableCell>{labels.kind[r.kind] ?? r.kind}</TableCell>
                  <TableCell>
                    <Badge variant={r.mode === 'enforce' ? 'default' : 'secondary'}>
                      {labels.mode[r.mode] ?? r.mode}
                    </Badge>
                  </TableCell>
                  <TableCell>
                    <Badge variant={r.enabled ? 'outline' : 'ghost'}>
                      {r.enabled ? t('Enabled') : t('Disabled')}
                    </Badge>
                  </TableCell>
                  <TableCell className='space-x-1 text-right whitespace-nowrap'>
                    <Button
                      size='sm'
                      variant='ghost'
                      onClick={() => {
                        setServerError(null)
                        setDialog({ kind: 'edit', rule: r })
                      }}
                    >
                      {t('Edit')}
                    </Button>
                    <Button
                      size='sm'
                      variant='ghost'
                      onClick={() => setDialog({ kind: 'delete', rule: r })}
                    >
                      {t('Delete')}
                    </Button>
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        </div>
      )}

      {(dialog?.kind === 'create' || dialog?.kind === 'edit') && (
        <RuleDialog
          rule={dialog.kind === 'edit' ? dialog.rule : undefined}
          view={view}
          saving={save.isPending}
          serverError={serverError}
          onClose={() => setDialog(null)}
          onSubmit={(body) =>
            save.mutate({
              id: dialog.kind === 'edit' ? dialog.rule.id : undefined,
              body,
            })
          }
        />
      )}
      <ConfirmDialog
        open={dialog?.kind === 'delete'}
        onOpenChange={(open) => {
          if (!open) setDialog(null)
        }}
        title={t('Delete this rule?')}
        desc={
          dialog?.kind === 'delete'
            ? t('"{{name}}" stops applying to requests right away.', {
                name: dialog.rule.name,
              })
            : ''
        }
        confirmText={t('Delete')}
        destructive
        isLoading={remove.isPending}
        handleConfirm={() => {
          if (dialog?.kind === 'delete') remove.mutate(dialog.rule.id)
        }}
      />
    </div>
  )
}
