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
import type {
  OnChangeFn,
  PaginationState,
  RowSelectionState,
} from '@tanstack/react-table'
import { KeyRound, Plus } from 'lucide-react'
import { useCallback, useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { ConfirmDialog } from '@/components/confirm-dialog'
import { EmptyState } from '@/components/empty-state'
import { ErrorState } from '@/components/error-state'
import { SectionPageLayout } from '@/components/section-page-layout'
import { LoadingState } from '@/components/loading-state'
import { Button } from '@/components/ui/button'
import { toApiError } from '@/lib/api'
import { useMoneyConfig } from '@/lib/status'

import {
  createToken,
  deleteToken,
  deleteTokens,
  keysQueryKey,
  projectsQueryOptions,
  rotateToken,
  tokenListQueryOptions,
  updateToken,
} from './api'
import { ApiAddresses } from './components/api-addresses'
import { KeyFormSheet } from './components/key-form-sheet'
import { KeySecretDialog } from './components/key-secret-dialog'
import type { KeyRowHandlers } from './components/key-row-actions'
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
  | { kind: 'delete-many'; ids: number[] }
  | null

const DEFAULT_PAGE_SIZE = 20

export function KeysPage() {
  const { t } = useTranslation()
  const qc = useQueryClient()
  const money = useMoneyConfig()
  const [pagination, setPagination] = useState<PaginationState>({
    pageIndex: 0,
    pageSize: DEFAULT_PAGE_SIZE,
  })
  const [dialog, setDialog] = useState<Dialog>(null)
  const [rowSelection, setRowSelection] = useState<RowSelectionState>({})
  const [busyId, setBusyId] = useState<number | null>(null)
  // Plaintext keys exist only in memory, from create / rotate; the list
  // endpoint returns masked keys, so a reload forgets them by design.
  const [secrets, setSecrets] = useState<Record<number, string>>({})

  const list = useQuery({
    ...tokenListQueryOptions(pagination.pageIndex + 1, pagination.pageSize),
    placeholderData: keepPreviousData,
  })
  const projects = useQuery(projectsQueryOptions)

  const projectNames = useMemo(
    () => new Map((projects.data ?? []).map((p) => [p.id, p.name])),
    [projects.data]
  )

  // Selection is per page: a batch delete is capped at 100 ids server-side.
  const onPaginationChange: OnChangeFn<PaginationState> = useCallback(
    (updater) => {
      setRowSelection({})
      setPagination((prev) =>
        typeof updater === 'function' ? updater(prev) : updater
      )
    },
    []
  )
  // After a delete the current page may no longer exist: step back.
  const ensurePageInRange = useCallback((pageCount: number) => {
    const last = Math.max(0, pageCount - 1)
    setPagination((prev) =>
      prev.pageIndex > last ? { ...prev, pageIndex: last } : prev
    )
  }, [])

  const refresh = useCallback(
    () => qc.invalidateQueries({ queryKey: keysQueryKey }),
    [qc]
  )
  const fail = (e: unknown) => {
    toast.error(toApiError(e).message)
  }

  const tokens = list.data?.items ?? []
  const total = list.data?.total ?? 0

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
    onSuccess: async (_d, v) => {
      toast.success(v.enable ? t('Key enabled') : t('Key disabled'))
      await refresh()
    },
    onError: fail,
  })
  const { mutate: mutateToggle } = toggle
  const onToggleStatus = useCallback(
    (token: ApiToken, enable: boolean) => mutateToggle({ token, enable }),
    [mutateToggle]
  )

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
      setRowSelection((s) => {
        const next = { ...s }
        delete next[String(token.id)]
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
      setRowSelection({})
      setDialog(null)
      await refresh()
    },
    onError: fail,
  })

  const handlers = useMemo<KeyRowHandlers>(
    () => ({
      onEdit: (token) => setDialog({ kind: 'edit', token }),
      onSnippets: (token) => setDialog({ kind: 'snippets', token }),
      onRotate: (token) => setDialog({ kind: 'rotate', token }),
      onDelete: (token) => setDialog({ kind: 'delete', token }),
    }),
    []
  )
  const onBulkDelete = useCallback(
    (ids: number[]) => setDialog({ kind: 'delete-many', ids }),
    []
  )

  const createButton = (
    <Button size='sm' onClick={() => setDialog({ kind: 'create' })}>
      <Plus />
      {t('Create key')}
    </Button>
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
  } else if (tokens.length === 0 && total === 0 && pagination.pageIndex === 0) {
    body = (
      <EmptyState
        icon={KeyRound}
        title={t('No API keys yet')}
        description={t('Create a key to start calling the models.')}
        bordered
        action={createButton}
      />
    )
  } else {
    body = (
      <div className='flex h-full min-h-0 flex-col gap-2'>
        {list.isError && (
          <p role='alert' className='text-destructive shrink-0 text-sm'>
            {t('Refreshing the list failed: {{message}}', {
              message: toApiError(list.error).message,
            })}
          </p>
        )}
        <div className='min-h-0 min-w-0 flex-1' data-testid='keys-body'>
          <KeysTable
            tokens={tokens}
            total={total}
            isLoading={false}
            isFetching={list.isFetching}
            money={money}
            secrets={secrets}
            busyId={busyId}
            projectNames={projectNames}
            pagination={pagination}
            onPaginationChange={onPaginationChange}
            ensurePageInRange={ensurePageInRange}
            rowSelection={rowSelection}
            onRowSelectionChange={setRowSelection}
            onToggleStatus={onToggleStatus}
            handlers={handlers}
            onBulkDelete={onBulkDelete}
          />
        </div>
      </div>
    )
  }

  const confirmRotate = dialog?.kind === 'rotate' ? dialog.token : null
  const confirmDelete = dialog?.kind === 'delete' ? dialog.token : null
  const confirmMany = dialog?.kind === 'delete-many' ? dialog.ids : null

  return (
    <>
      <SectionPageLayout fixedContent>
        <SectionPageLayout.Title>{t('API Keys')}</SectionPageLayout.Title>
        <SectionPageLayout.Actions>
          <ApiAddresses />
          {createButton}
        </SectionPageLayout.Actions>
        <SectionPageLayout.Content>{body}</SectionPageLayout.Content>
      </SectionPageLayout>

      {dialog?.kind === 'create' && (
        <KeyFormSheet
          money={money}
          projects={projects.data ?? []}
          projectsFailed={projects.isError}
          saving={create.isPending}
          onSubmit={(b) => create.mutate(b)}
          onClose={() => setDialog(null)}
        />
      )}
      {dialog?.kind === 'edit' && (
        <KeyFormSheet
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
        open={confirmMany !== null}
        onOpenChange={(open) => {
          if (!open) setDialog(null)
        }}
        title={t('Delete the selected keys?')}
        desc={t('{{count}} keys will be deleted. This cannot be undone.', {
          count: confirmMany?.length ?? 0,
        })}
        confirmText={t('Delete')}
        destructive
        isLoading={removeMany.isPending}
        handleConfirm={() => confirmMany && removeMany.mutate(confirmMany)}
      />
    </>
  )
}
