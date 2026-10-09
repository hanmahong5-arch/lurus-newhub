import {
  useMutation,
  useQuery,
  useQueryClient,
} from '@tanstack/react-query'
import { useState, type FormEvent } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { ErrorState } from '@/components/error-state'
import { LoadingState } from '@/components/loading-state'
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
  addMember,
  departmentsQueryKey,
  membersQueryOptions,
  removeMember,
} from '../api'
import { parseUserId } from '../lib/form'
import type { Department, DepartmentMember } from '../types'

interface Props {
  department: Department
  onClose: () => void
}


export function MembersDialog({ department, onClose }: Props) {
  const { t } = useTranslation()
  const qc = useQueryClient()
  const members = useQuery(membersQueryOptions(department.id))
  const [userId, setUserId] = useState('')
  const [invalid, setInvalid] = useState(false)
  const [removing, setRemoving] = useState<DepartmentMember | null>(null)

  const refresh = () =>
    qc.invalidateQueries({
      queryKey: [...departmentsQueryKey, 'members', department.id],
    })
  const fail = (e: unknown) => {
    toast.error(toApiError(e).message)
  }

  const add = useMutation({
    mutationFn: (id: number) => addMember(department.id, id),
    onSuccess: async () => {
      toast.success(t('Member added'))
      setUserId('')
      await refresh()
    },
    onError: fail,
  })

  const remove = useMutation({
    mutationFn: (id: number) => removeMember(department.id, id),
    onSuccess: async () => {
      toast.success(t('Member removed'))
      setRemoving(null)
      await refresh()
    },
    onError: fail,
  })

  const submit = (e: FormEvent) => {
    e.preventDefault()
    if (add.isPending) return
    const id = parseUserId(userId)
    if (id === null) {
      setInvalid(true)
      return
    }
    add.mutate(id)
  }

  const roleLabel = (role: string) => {
    if (role === 'dept_lead') return t('Department lead')
    if (role === 'admin') return t('Admin')
    return t('Member')
  }

  let body
  if (members.isPending) {
    body = <LoadingState />
  } else if (members.isError) {
    body = (
      <ErrorState
        title={t('Could not load the members')}
        description={toApiError(members.error).message}
        onRetry={() => void members.refetch()}
      />
    )
  } else if (members.data.length === 0) {
    body = (
      <p className='text-muted-foreground text-sm'>
        {t('This department has no members yet.')}
      </p>
    )
  } else {
    body = (
      <Table>
        <TableHeader>
          <TableRow>
            <TableHead>{t('User')}</TableHead>
            <TableHead>{t('Role')}</TableHead>
            <TableHead className='text-end'>{t('Actions')}</TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          {members.data.map((m) => (
            <TableRow key={m.userId} data-testid={`member-row-${m.userId}`}>
              <TableCell>
                <div className='font-medium'>
                  {m.displayName !== '' ? m.displayName : m.username}
                </div>
                <div className='text-muted-foreground text-xs'>
                  {m.username} (#{m.userId})
                </div>
              </TableCell>
              <TableCell>{roleLabel(m.tenantRole)}</TableCell>
              <TableCell className='text-end'>
                {removing?.userId === m.userId ? (
                  <span className='inline-flex items-center gap-1'>
                    <Button
                      variant='destructive'
                      size='sm'
                      disabled={remove.isPending}
                      onClick={() => remove.mutate(m.userId)}
                    >
                      {t('Confirm remove')}
                    </Button>
                    <Button
                      variant='outline'
                      size='sm'
                      disabled={remove.isPending}
                      onClick={() => setRemoving(null)}
                    >
                      {t('Cancel')}
                    </Button>
                  </span>
                ) : (
                  <Button
                    variant='ghost'
                    size='sm'
                    onClick={() => setRemoving(m)}
                    aria-label={t('Remove {{name}}', { name: m.username })}
                  >
                    {t('Remove')}
                  </Button>
                )}
              </TableCell>
            </TableRow>
          ))}
        </TableBody>
      </Table>
    )
  }

  return (
    <Dialog
      open
      onOpenChange={(open) => {
        if (!open) onClose()
      }}
    >
      <DialogContent className='sm:max-w-xl'>
        <DialogHeader>
          <DialogTitle>
            {t('Members of {{name}}', { name: department.name })}
          </DialogTitle>
          <DialogDescription>
            {t(
              'A member with no role yet becomes a department lead and can see this department. Removing a lead from their last department revokes the role.'
            )}
          </DialogDescription>
        </DialogHeader>

        <form onSubmit={submit} className='flex items-start gap-2' noValidate>
          <div className='grid flex-1 gap-1'>
            <Input
              inputMode='numeric'
              value={userId}
              onChange={(e) => {
                setUserId(e.target.value)
                setInvalid(false)
              }}
              placeholder={t('User ID')}
              aria-label={t('User ID')}
            />
            {invalid && (
              <p role='alert' className='text-destructive text-xs'>
                {t('Enter a numeric user ID.')}
              </p>
            )}
          </div>
          <Button type='submit' disabled={add.isPending}>
            {t('Add member')}
          </Button>
        </form>

        <div className='max-h-80 overflow-y-auto'>{body}</div>

        <DialogFooter>
          <Button variant='outline' onClick={onClose}>
            {t('Close')}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
