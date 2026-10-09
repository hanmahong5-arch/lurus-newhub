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

import { buildInviteBody } from '../lib/invite-body'
import { DEFAULT_TTL_HOURS, role as toRole } from '../lib/map'
import type { InviteWriteBody, MemberRole, ProjectRef } from '../types'

interface InviteFormDialogProps {
  projects: ProjectRef[]
  projectsFailed: boolean
  saving: boolean
  error: string | null
  onSubmit: (body: InviteWriteBody) => void
  onClose: () => void
}

export function InviteFormDialog(props: InviteFormDialogProps) {
  const { t } = useTranslation()
  const [role, setRole] = useState<MemberRole>('')
  const [projectId, setProjectId] = useState('')
  const [ttl, setTtl] = useState(String(DEFAULT_TTL_HOURS))
  const [invalid, setInvalid] = useState(false)

  const submit = (e: FormEvent) => {
    e.preventDefault()
    if (props.saving) return
    const body = buildInviteBody(ttl, role, projectId)
    if (!body) {
      setInvalid(true)
      return
    }
    props.onSubmit(body)
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
          <DialogTitle>{t('Create invitation')}</DialogTitle>
          <DialogDescription>
            {t(
              'Whoever redeems the code joins this organization with the role and department below.'
            )}
          </DialogDescription>
        </DialogHeader>
        <form onSubmit={submit} className='grid gap-4'>
          <div className='grid gap-1.5'>
            <Label>{t('Role')}</Label>
            <NativeSelect
              className='w-full'
              value={role}
              onChange={(e) => setRole(toRole(e.target.value))}
              aria-label={t('Role')}
            >
              <NativeSelectOption value=''>{t('Member')}</NativeSelectOption>
              <NativeSelectOption value='dept_lead'>
                {t('Department lead')}
              </NativeSelectOption>
              <NativeSelectOption value='admin'>
                {t('Administrator')}
              </NativeSelectOption>
            </NativeSelect>
          </div>
          {props.projectsFailed ? (
            <p role='alert' className='text-destructive text-sm'>
              {t('Could not load departments; the invitation will not be bound to one.')}
            </p>
          ) : (
            <div className='grid gap-1.5'>
              <Label>{t('Department')}</Label>
              <NativeSelect
                className='w-full'
                value={projectId}
                onChange={(e) => setProjectId(e.target.value)}
                aria-label={t('Department')}
              >
                <NativeSelectOption value=''>{t('None')}</NativeSelectOption>
                {props.projects.map((p) => (
                  <NativeSelectOption key={p.id} value={String(p.id)}>
                    {p.name}
                  </NativeSelectOption>
                ))}
              </NativeSelect>
            </div>
          )}
          <div className='grid gap-1.5'>
            <Label htmlFor='invite-ttl'>{t('Valid for (hours)')}</Label>
            <Input
              id='invite-ttl'
              inputMode='numeric'
              value={ttl}
              onChange={(e) => {
                setTtl(e.target.value)
                setInvalid(false)
              }}
            />
            <p className='text-muted-foreground text-xs'>
              {t('Default 72 hours, at most 720 hours (30 days).')}
            </p>
          </div>
          {invalid && (
            <p role='alert' className='text-destructive text-sm'>
              {t('Enter a whole number of hours between 1 and 720.')}
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
              {t('Create')}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}
