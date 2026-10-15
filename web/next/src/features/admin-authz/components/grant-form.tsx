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
import { useMutation, useQueryClient } from '@tanstack/react-query'
import { Plus } from 'lucide-react'
import { useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { Button } from '@/components/ui/button'
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from '@/components/ui/card'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { NativeSelect, NativeSelectOption } from '@/components/ui/native-select'
import { getServerErrorMessage } from '@/lib/server-error-message'

import { authzKeys, createGrant } from '../api'
import {
  EMPTY_FORM,
  MAX_TTL_SECONDS,
  parseGrantForm,
  type GrantFormValues,
} from '../lib/grants'
import type { AuthzCatalog } from '../types'

export function GrantForm(props: { catalog: AuthzCatalog }) {
  const { t } = useTranslation()
  const qc = useQueryClient()
  const [form, setForm] = useState<GrantFormValues>(EMPTY_FORM)

  const actions = useMemo(
    () =>
      props.catalog.resources.find((r) => r.resource === form.resource)
        ?.actions ?? [],
    [props.catalog, form.resource]
  )

  const create = useMutation({
    mutationFn: createGrant,
    // This form presents its own failure; a second global toast would stack.
    meta: { errorToast: false },
    onSuccess: async () => {
      toast.success(t('Grant created'))
      setForm(EMPTY_FORM)
      await qc.invalidateQueries({ queryKey: authzKeys.grants })
    },
    onError: (error) => {
      // 409 GRANT_EXISTS / 400 GRANT_INVALID carry the reason in the message.
      toast.error(getServerErrorMessage(error, t('Failed to create grant')))
    },
  })

  const ready =
    !create.isPending &&
    form.userId !== '' &&
    form.resource !== '' &&
    form.action !== ''

  const submit = (e: React.FormEvent) => {
    e.preventDefault()
    const input = parseGrantForm(form)
    if (!input || create.isPending) return
    create.mutate(input)
  }

  return (
    <Card>
      <CardHeader>
        <CardTitle>{t('New grant')}</CardTitle>
        <CardDescription>
          {t(
            'Grant a narrow root-gated permission to an admin without giving them root. Grants are global.'
          )}
        </CardDescription>
      </CardHeader>
      <CardContent>
        <form
          className='grid gap-3 sm:grid-cols-2 lg:grid-cols-[repeat(4,minmax(0,1fr))_auto] lg:items-end'
          onSubmit={submit}
          data-testid='authz-create-form'
        >
          <div className='flex flex-col gap-1.5'>
            <Label htmlFor='authz-user-id'>{t('User ID')}</Label>
            <Input
              id='authz-user-id'
              type='number'
              inputMode='numeric'
              min={1}
              placeholder={t('User ID')}
              value={form.userId}
              onChange={(e) =>
                setForm((f) => ({ ...f, userId: e.target.value }))
              }
            />
          </div>
          <div className='flex flex-col gap-1.5'>
            <Label htmlFor='authz-resource'>{t('Resource')}</Label>
            <NativeSelect
              id='authz-resource'
              className='w-full'
              value={form.resource}
              onChange={(e) =>
                setForm((f) => ({ ...f, resource: e.target.value, action: '' }))
              }
            >
              <NativeSelectOption value=''>
                {t('Select resource')}
              </NativeSelectOption>
              {props.catalog.resources.map((r) => (
                <NativeSelectOption key={r.resource} value={r.resource}>
                  {r.resource}
                </NativeSelectOption>
              ))}
            </NativeSelect>
          </div>
          <div className='flex flex-col gap-1.5'>
            <Label htmlFor='authz-action'>{t('Action')}</Label>
            <NativeSelect
              id='authz-action'
              className='w-full'
              value={form.action}
              disabled={!form.resource}
              onChange={(e) =>
                setForm((f) => ({ ...f, action: e.target.value }))
              }
            >
              <NativeSelectOption value=''>
                {t('Select action')}
              </NativeSelectOption>
              {actions.map((a) => (
                <NativeSelectOption key={a} value={a}>
                  {a}
                </NativeSelectOption>
              ))}
            </NativeSelect>
          </div>
          <div className='flex flex-col gap-1.5'>
            <Label htmlFor='authz-ttl'>{t('Lifetime (seconds)')}</Label>
            <Input
              id='authz-ttl'
              type='number'
              inputMode='numeric'
              min={1}
              max={MAX_TTL_SECONDS}
              placeholder={t('Optional, max 90 days')}
              value={form.ttlSeconds}
              onChange={(e) =>
                setForm((f) => ({ ...f, ttlSeconds: e.target.value }))
              }
            />
          </div>
          <Button
            type='submit'
            disabled={!ready}
            className='sm:col-span-2 lg:col-span-1'
          >
            <Plus className='size-4' />
            {create.isPending ? t('Granting...') : t('Grant permission')}
          </Button>
        </form>
        <p className='text-muted-foreground mt-3 text-xs'>
          {t('Leave the lifetime empty for a permanent grant.')}
        </p>
      </CardContent>
    </Card>
  )
}
