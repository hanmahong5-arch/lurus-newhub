import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Plus, ShieldCheck } from 'lucide-react'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { ConfirmDialog } from '@/components/confirm-dialog'
import { EmptyState } from '@/components/empty-state'
import { ErrorState } from '@/components/error-state'
import { LoadingState } from '@/components/loading-state'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
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
  rulesQueryKey,
  rulesQueryOptions,
  updateRule,
} from '../api'
import { bodyFromRule } from '../lib/rule-form'
import type { ContentRule, RuleWriteBody } from '../types'
import { useRuleLabels } from './labels'
import { RuleFormDialog } from './rule-form-dialog'

type Dialog =
  | { kind: 'create' }
  | { kind: 'edit'; rule: ContentRule }
  | { kind: 'delete'; rule: ContentRule }
  | { kind: 'enforce'; rule: ContentRule }
  | null

export function RulesPanel() {
  const { t } = useTranslation()
  const labels = useRuleLabels()
  const qc = useQueryClient()
  const [dialog, setDialog] = useState<Dialog>(null)
  const [busyId, setBusyId] = useState<number | null>(null)

  const list = useQuery(rulesQueryOptions)
  const refresh = () => qc.invalidateQueries({ queryKey: rulesQueryKey })
  const fail = (e: unknown) => {
    toast.error(toApiError(e).message)
  }

  const create = useMutation({
    mutationFn: (body: RuleWriteBody) => createRule(body),
    onSuccess: async () => {
      toast.success(t('Rule created'))
      setDialog(null)
      await refresh()
    },
    onError: fail,
  })

  const edit = useMutation({
    mutationFn: (v: { id: number; body: RuleWriteBody }) =>
      updateRule(v.id, v.body),
    onSuccess: async () => {
      toast.success(t('Saved'))
      setDialog(null)
      await refresh()
    },
    onError: fail,
  })

  // Mode / enabled flips from the table; the PUT needs the whole rule.
  const patch = useMutation({
    mutationFn: (v: { rule: ContentRule; patch: Partial<RuleWriteBody> }) =>
      updateRule(v.rule.id, bodyFromRule(v.rule, v.patch)),
    onMutate: (v) => setBusyId(v.rule.id),
    onSettled: () => setBusyId(null),
    onSuccess: async () => {
      setDialog(null)
      await refresh()
    },
    onError: fail,
  })

  const remove = useMutation({
    mutationFn: (rule: ContentRule) => deleteRule(rule.id),
    onSuccess: async () => {
      toast.success(t('Rule deleted'))
      setDialog(null)
      await refresh()
    },
    onError: fail,
  })

  const data = list.data
  const rules = data?.rules ?? []
  const atLimit =
    data != null && data.max_rules > 0 && rules.length >= data.max_rules

  const addButton = (
    <Button
      onClick={() => setDialog({ kind: 'create' })}
      disabled={atLimit || data === undefined}
    >
      <Plus />
      {t('Add rule')}
    </Button>
  )

  let body
  if (list.isPending) {
    body = <LoadingState />
  } else if (list.isError && data === undefined) {
    // A failed read is an error, never an empty list.
    body = (
      <ErrorState
        title={t('Could not load the content rules')}
        description={toApiError(list.error).message}
        onRetry={() => void list.refetch()}
      />
    )
  } else if (rules.length === 0) {
    body = (
      <EmptyState
        icon={ShieldCheck}
        title={t('No platform content rules')}
        description={t(
          'Rules added here apply to every tenant, in addition to their own rules.'
        )}
        bordered
        action={addButton}
      />
    )
  } else {
    body = (
      <div className='min-w-0 rounded-lg border' data-testid='rules-table'>
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>{t('Order')}</TableHead>
              <TableHead>{t('Name')}</TableHead>
              <TableHead>{t('Applies to')}</TableHead>
              <TableHead>{t('Action')}</TableHead>
              <TableHead>{t('Pattern')}</TableHead>
              <TableHead>{t('Enforce')}</TableHead>
              <TableHead>{t('Enabled')}</TableHead>
              <TableHead className='text-right'>{t('Actions')}</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {rules.map((rule) => {
              const busy = busyId === rule.id
              const enforcing = rule.mode === 'enforce'
              return (
                <TableRow key={rule.id}>
                  <TableCell>{rule.ordinal}</TableCell>
                  <TableCell className='font-medium'>{rule.name}</TableCell>
                  <TableCell>{labels.role(rule.role_scope)}</TableCell>
                  <TableCell>{labels.kind(rule.kind)}</TableCell>
                  <TableCell className='max-w-64 truncate font-mono text-xs'>
                    {rule.pattern_type === 'builtin' ? (
                      <Badge variant='outline'>{rule.builtin}</Badge>
                    ) : (
                      rule.pattern
                    )}
                  </TableCell>
                  <TableCell>
                    <div className='flex items-center gap-2'>
                      <Switch
                        checked={enforcing}
                        disabled={busy}
                        aria-label={t('Enforce {{name}}', { name: rule.name })}
                        onCheckedChange={(on) => {
                          if (on) {
                            setDialog({ kind: 'enforce', rule })
                          } else {
                            patch.mutate({ rule, patch: { mode: 'observe' } })
                          }
                        }}
                      />
                      <Badge variant={enforcing ? 'default' : 'secondary'}>
                        {labels.mode(rule.mode)}
                      </Badge>
                    </div>
                  </TableCell>
                  <TableCell>
                    <Switch
                      checked={rule.enabled}
                      disabled={busy}
                      aria-label={t('Enable {{name}}', { name: rule.name })}
                      onCheckedChange={(on) =>
                        patch.mutate({ rule, patch: { enabled: on } })
                      }
                    />
                  </TableCell>
                  <TableCell className='text-right'>
                    <div className='flex justify-end gap-1'>
                      <Button
                        variant='ghost'
                        size='sm'
                        onClick={() => setDialog({ kind: 'edit', rule })}
                      >
                        {t('Edit')}
                      </Button>
                      <Button
                        variant='ghost'
                        size='sm'
                        className='text-destructive'
                        onClick={() => setDialog({ kind: 'delete', rule })}
                      >
                        {t('Delete')}
                      </Button>
                    </div>
                  </TableCell>
                </TableRow>
              )
            })}
          </TableBody>
        </Table>
        {list.isError && (
          <p role='alert' className='text-destructive p-3 text-sm'>
            {t('Refreshing the list failed: {{message}}', {
              message: toApiError(list.error).message,
            })}
          </p>
        )}
      </div>
    )
  }

  const confirmDelete = dialog?.kind === 'delete' ? dialog.rule : null
  const confirmEnforce = dialog?.kind === 'enforce' ? dialog.rule : null

  return (
    <div className='grid gap-3'>
      <div className='flex flex-wrap items-center justify-between gap-2'>
        <p className='text-muted-foreground text-sm'>
          {data != null && data.max_rules > 0
            ? t('{{count}} of {{max}} rules used', {
                count: rules.length,
                max: data.max_rules,
              })
            : t('Rules that apply to every tenant.')}
        </p>
        {rules.length > 0 && addButton}
      </div>
      {body}

      {dialog?.kind === 'create' && (
        <RuleFormDialog
          builtins={data?.builtins ?? []}
          maxPatternLen={data?.max_pattern_len ?? 0}
          saving={create.isPending}
          onSubmit={(b) => create.mutate(b)}
          onClose={() => setDialog(null)}
        />
      )}
      {dialog?.kind === 'edit' && (
        <RuleFormDialog
          key={dialog.rule.id}
          rule={dialog.rule}
          builtins={data?.builtins ?? []}
          maxPatternLen={data?.max_pattern_len ?? 0}
          saving={edit.isPending}
          onSubmit={(b) => edit.mutate({ id: dialog.rule.id, body: b })}
          onClose={() => setDialog(null)}
        />
      )}

      <ConfirmDialog
        open={confirmEnforce !== null}
        onOpenChange={(open) => {
          if (!open) setDialog(null)
        }}
        title={t('Switch this rule to enforce?')}
        desc={t(
          'In enforce mode matches are masked or rejected for every tenant, not just recorded.'
        )}
        confirmText={t('Enforce')}
        isLoading={patch.isPending}
        handleConfirm={() =>
          confirmEnforce &&
          patch.mutate({ rule: confirmEnforce, patch: { mode: 'enforce' } })
        }
      />
      <ConfirmDialog
        open={confirmDelete !== null}
        onOpenChange={(open) => {
          if (!open) setDialog(null)
        }}
        title={t('Delete this rule?')}
        desc={t(
          'The rule stops applying to every tenant. This cannot be undone.'
        )}
        confirmText={t('Delete')}
        destructive
        isLoading={remove.isPending}
        handleConfirm={() => confirmDelete && remove.mutate(confirmDelete)}
      />
    </div>
  )
}
