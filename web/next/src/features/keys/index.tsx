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
import {
  keepPreviousData,
  useMutation,
  useQuery,
  useQueryClient,
} from '@tanstack/react-query'
import { KeyRound, Plus } from 'lucide-react'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { ConfirmDialog } from '@/components/confirm-dialog'
import { EmptyState } from '@/components/empty-state'
import { ErrorState } from '@/components/error-state'
import { PageHeader } from '@/components/layout/page-header'
import { LoadingState } from '@/components/loading-state'
import { Button } from '@/components/ui/button'
import { toApiError } from '@/lib/api'
import { useMoneyConfig } from '@/lib/status'

import {
  PAGE_SIZE,
  createToken,
  deleteToken,
  deleteTokens,
  keysQueryKey,
  projectsQueryOptions,
  rotateToken,
  tokenListQueryOptions,
  updateToken,
} from './api'
import { KeyFormDialog } from './components/key-form-dialog'
import { KeySecretDialog } from './components/key-secret-dialog'
import { KeysTable } from './components/keys-table'
import { SnippetsDialog } from './components/snippets-dialog'
import {
  TOKEN_STATUS_DISABLED,
  TOKEN_STATUS_ENABLED,
  type ApiToken,
  type IssuedKey,
  type TokenWriteBody,
} from './types'

type Dialog =
  | { kind: 'create' }
  | { kind: 'edit'; token: ApiToken }
  | { kind: 'secret'; token: ApiToken | null; issued: IssuedKey; rotated: boolean }
  | { kind: 'snippets'; token: ApiToken }
  | { kind: 'rotate'; token: ApiToken }
  | { kind: 'delete'; token: ApiToken }
  | { kind: 'delete-many' }
  | null

export function KeysPage() {
  const { t } = useTranslation()
  const qc = useQueryClient()
  const money = useMoneyConfig()
  const [page, setPage] = useState(1)
  const [dialog, setDialog] = useState<Dialog>(null)
  const [selected, setSelected] = useState<Set<number>>(new Set())
  const [busyId, setBusyId] = useState<number | null>(null)
  // Plaintext keys exist only in memory, from create / rotate; the list
  // endpoint returns masked keys, so a reload forgets them by design.
  const [secrets, setSecrets] = useState<Record<number, string>>({})

  const list = useQuery({
    ...tokenListQueryOptions(page, PAGE_SIZE),
    placeholderData: keepPreviousData,
  })
  const projects = useQuery(projectsQueryOptions)

  // Selection is per page: a batch delete is capped at 100 ids server-side.
  const goPage = (next: number) => {
    setSelected(new Set())
    setPage(next)
  }

  const refresh = () => qc.invalidateQueries({ queryKey: keysQueryKey })
  const fail = (e: unknown) => {
    toast.error(toApiError(e).message)
  }

  const tokens = list.data?.items ?? []
  const total = list.data?.total ?? 0
  const pageCount = Math.max(1, Math.ceil(total / PAGE_SIZE))
  const nowSec = Math.floor(Date.now() / 1000)

  const remember = (issued: IssuedKey) =>
    setSecrets((s) => ({ ...s, [issued.id]: issued.key }))

  const create = useMutation({
    mutationFn: (body: TokenWriteBody) => createToken(body),
    onSuccess: async (issued) => {
      remember(issued)
      await refresh()
      setDialog({ kind: 'secret', token: null, issued, rotated: false })
    },
    onError: fail,
  })

  const edit = useMutation({
    mutationFn: (v: { id: number; body: TokenWriteBody }) =>
      updateToken(v.id, v.body),
    onSuccess: async () => {
      toast.success(t('Saved'))
      setDialog(null)
      await refresh()
    },
    onError: fail,
  })

  const toggle = useMutation({
    mutationFn: (v: { token: ApiToken; enable: boolean }) =>
      updateToken(v.token.id, {
        status: v.enable ? TOKEN_STATUS_ENABLED : TOKEN_STATUS_DISABLED,
      }),
    onMutate: (v) => setBusyId(v.token.id),
    onSettled: () => setBusyId(null),
    onSuccess: refresh,
    onError: fail,
  })

  const rotate = useMutation({
    mutationFn: (token: ApiToken) => rotateToken(token.id),
    onSuccess: async (issued, token) => {
      remember(issued)
      await refresh()
      setDialog({ kind: 'secret', token, issued, rotated: true })
    },
    onError: fail,
  })

  const remove = useMutation({
    mutationFn: (token: ApiToken) => deleteToken(token.id),
    onSuccess: async (_d, token) => {
      toast.success(t('Key deleted'))
      setSelected((s) => {
        const next = new Set(s)
        next.delete(token.id)
        return next
      })
      setDialog(null)
      await refresh()
    },
    onError: fail,
  })

  const removeMany = useMutation({
    mutationFn: (ids: number[]) => deleteTokens(ids),
    onSuccess: async () => {
      toast.success(t('Keys deleted'))
      setSelected(new Set())
      setDialog(null)
      await refresh()
    },
    onError: fail,
  })

  const header = (
    <PageHeader
      title={t('API Keys')}
      description={t('Keys your applications use to call the models.')}
      actions={
        <div className='flex flex-wrap gap-2'>
          {selected.size > 0 && (
            <Button
              variant='destructive'
              onClick={() => setDialog({ kind: 'delete-many' })}
            >
              {t('Delete selected ({{count}})', { count: selected.size })}
            </Button>
          )}
          <Button onClick={() => setDialog({ kind: 'create' })}>
            <Plus />
            {t('Create key')}
          </Button>
        </div>
      }
    />
  )

  let body
  if (list.isPending) {
    body = <LoadingState />
  } else if (list.isError && list.data === undefined) {
    // A failed read is an error, never an empty list.
    body = (
      <ErrorState
        title={t('Could not load your keys')}
        description={toApiError(list.error).message}
        onRetry={() => void list.refetch()}
      />
    )
  } else if (tokens.length === 0 && page === 1) {
    body = (
      <EmptyState
        icon={KeyRound}
        title={t('No API keys yet')}
        description={t('Create a key to start calling the models.')}
        bordered
        action={
          <Button onClick={() => setDialog({ kind: 'create' })}>
            <Plus />
            {t('Create key')}
          </Button>
        }
      />
    )
  } else {
    body = (
      <div className='grid min-w-0 gap-3' data-testid='keys-body'>
        <div className='min-w-0 rounded-lg border' data-testid='keys-table-box'>
          <KeysTable
            tokens={tokens}
            money={money}
            nowSec={nowSec}
            secrets={secrets}
            selected={selected}
            busyId={busyId}
            onToggleSelect={(id, on) =>
              setSelected((s) => {
                const next = new Set(s)
                if (on) next.add(id)
                else next.delete(id)
                return next
              })
            }
            onToggleAll={(on) =>
              setSelected((s) => {
                const next = new Set(s)
                for (const k of tokens) {
                  if (on) next.add(k.id)
                  else next.delete(k.id)
                }
                return next
              })
            }
            onToggleStatus={(token, enable) => toggle.mutate({ token, enable })}
            onEdit={(token) => setDialog({ kind: 'edit', token })}
            onSnippets={(token) => setDialog({ kind: 'snippets', token })}
            onRotate={(token) => setDialog({ kind: 'rotate', token })}
            onDelete={(token) => setDialog({ kind: 'delete', token })}
          />
        </div>
        <div className='flex flex-wrap items-center justify-between gap-2 text-sm'>
          <span className='text-muted-foreground'>
            {t('{{count}} keys in total', { count: total })}
          </span>
          <div className='flex items-center gap-2'>
            <Button
              variant='outline'
              size='sm'
              disabled={page <= 1}
              onClick={() => goPage(Math.max(1, page - 1))}
            >
              {t('Previous')}
            </Button>
            <span>
              {t('Page {{page}} of {{pages}}', { page, pages: pageCount })}
            </span>
            <Button
              variant='outline'
              size='sm'
              disabled={page >= pageCount}
              onClick={() => goPage(page + 1)}
            >
              {t('Next')}
            </Button>
          </div>
        </div>
        {list.isError && (
          <p role='alert' className='text-destructive text-sm'>
            {t('Refreshing the list failed: {{message}}', {
              message: toApiError(list.error).message,
            })}
          </p>
        )}
      </div>
    )
  }

  const confirmRotate = dialog?.kind === 'rotate' ? dialog.token : null
  const confirmDelete = dialog?.kind === 'delete' ? dialog.token : null

  return (
    <>
      {header}
      {body}

      {dialog?.kind === 'create' && (
        <KeyFormDialog
          money={money}
          projects={projects.data ?? []}
          projectsFailed={projects.isError}
          saving={create.isPending}
          onSubmit={(b) => create.mutate(b)}
          onClose={() => setDialog(null)}
        />
      )}
      {dialog?.kind === 'edit' && (
        <KeyFormDialog
          key={dialog.token.id}
          token={dialog.token}
          money={money}
          projects={projects.data ?? []}
          projectsFailed={projects.isError}
          saving={edit.isPending}
          onSubmit={(b) => edit.mutate({ id: dialog.token.id, body: b })}
          onClose={() => setDialog(null)}
        />
      )}
      {dialog?.kind === 'secret' && (
        <KeySecretDialog
          secret={dialog.issued.key}
          rotated={dialog.rotated}
          onShowSnippets={() => {
            const target =
              dialog.token ??
              tokens.find((k) => k.id === dialog.issued.id) ??
              null
            setDialog(target ? { kind: 'snippets', token: target } : null)
          }}
          onClose={() => setDialog(null)}
        />
      )}
      {dialog?.kind === 'snippets' && (
        <SnippetsDialog
          token={dialog.token}
          secret={secrets[dialog.token.id]}
          onClose={() => setDialog(null)}
        />
      )}

      <ConfirmDialog
        open={confirmRotate !== null}
        onOpenChange={(open) => {
          if (!open) setDialog(null)
        }}
        title={t('Rotate this key?')}
        desc={t(
          'A new key is issued and the current one stops working immediately. Update every application that uses it.'
        )}
        confirmText={t('Rotate')}
        isLoading={rotate.isPending}
        handleConfirm={() => confirmRotate && rotate.mutate(confirmRotate)}
      />
      <ConfirmDialog
        open={confirmDelete !== null}
        onOpenChange={(open) => {
          if (!open) setDialog(null)
        }}
        title={t('Delete this key?')}
        desc={t('Applications using it will stop working. This cannot be undone.')}
        confirmText={t('Delete')}
        destructive
        isLoading={remove.isPending}
        handleConfirm={() => confirmDelete && remove.mutate(confirmDelete)}
      />
      <ConfirmDialog
        open={dialog?.kind === 'delete-many'}
        onOpenChange={(open) => {
          if (!open) setDialog(null)
        }}
        title={t('Delete the selected keys?')}
        desc={t('{{count}} keys will be deleted. This cannot be undone.', {
          count: selected.size,
        })}
        confirmText={t('Delete')}
        destructive
        isLoading={removeMany.isPending}
        handleConfirm={() => removeMany.mutate([...selected])}
      />
    </>
  )
}
