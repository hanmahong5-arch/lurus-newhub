import { useState, type FormEvent } from 'react'
import { useTranslation } from 'react-i18next'

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

import { role as toRole } from '../lib/map'
import type { MemberRole } from '../types'

interface RoleDialogProps {
  /** Present = change this member; absent = enter a user id. */
  member?: { userId: number; name: string; role: MemberRole }
  saving: boolean
  error: string | null
  onSubmit: (userId: number, role: MemberRole) => void
  onClose: () => void
}

export function RoleDialog(props: RoleDialogProps) {
  const { t } = useTranslation()
  const { member } = props
  const [userId, setUserId] = useState(member ? String(member.userId) : '')
  const [role, setRole] = useState<MemberRole>(member?.role ?? '')
  const [invalid, setInvalid] = useState(false)

  const submit = (e: FormEvent) => {
    e.preventDefault()
    if (props.saving) return
    const id = Number(userId)
    if (!Number.isInteger(id) || id <= 0) {
      setInvalid(true)
      return
    }
    props.onSubmit(id, role)
  }

  return (
    <Dialog
      open
      onOpenChange={(open) => {
        if (!open && !props.saving) props.onClose()
      }}
    >
      <DialogContent className='sm:max-w-md'>
        <DialogHeader>
          <DialogTitle>{t('Change role')}</DialogTitle>
          <DialogDescription>
            {member
              ? t('Set the role of {{name}} in this organization.', {
                  name: member.name,
                })
              : t('Set the role of a member by their user ID.')}
          </DialogDescription>
        </DialogHeader>
        <form onSubmit={submit} className='grid gap-4'>
          {!member && (
            <div className='grid gap-1.5'>
              <Label htmlFor='role-user-id'>{t('User ID')}</Label>
              <Input
                id='role-user-id'
                inputMode='numeric'
                value={userId}
                onChange={(e) => {
                  setUserId(e.target.value)
                  setInvalid(false)
                }}
              />
            </div>
          )}
          <div className='grid gap-1.5'>
            <Label>{t('Role')}</Label>
            <NativeSelect
              className='w-full'
              value={role}
              onChange={(e) => setRole(toRole(e.target.value))}
              aria-label={t('Role')}
            >
              <NativeSelectOption value='admin'>
                {t('Administrator')}
              </NativeSelectOption>
              <NativeSelectOption value='dept_lead'>
                {t('Department lead')}
              </NativeSelectOption>
              <NativeSelectOption value=''>{t('Member')}</NativeSelectOption>
            </NativeSelect>
          </div>
          {invalid && (
            <p role='alert' className='text-destructive text-sm'>
              {t('Enter a valid user ID.')}
            </p>
          )}
          {props.error != null && (
            <p role='alert' className='text-destructive text-sm'>
              {props.error}
            </p>
          )}
          <DialogFooter>
            <Button
              type='button'
              variant='outline'
              disabled={props.saving}
              onClick={props.onClose}
            >
              {t('Cancel')}
            </Button>
            <Button type='submit' disabled={props.saving}>
              {t('Save')}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}
