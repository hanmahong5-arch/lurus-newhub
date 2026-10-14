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
import { zodResolver } from '@hookform/resolvers/zod'
import { useMutation, useQuery } from '@tanstack/react-query'
import { Coins } from 'lucide-react'
import { useEffect, useMemo, useRef } from 'react'
import { useForm } from 'react-hook-form'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import {
  SideDrawerSection,
  sideDrawerContentClassName,
  sideDrawerFooterClassName,
  sideDrawerFormClassName,
  sideDrawerHeaderClassName,
} from '@/components/drawer-layout'
import { Button } from '@/components/ui/button'
import { Combobox } from '@/components/ui/combobox'
import {
  Form,
  FormControl,
  FormDescription,
  FormField,
  FormItem,
  FormLabel,
  FormMessage,
} from '@/components/ui/form'
import { Input } from '@/components/ui/input'
import {
  Sheet,
  SheetClose,
  SheetContent,
  SheetDescription,
  SheetFooter,
  SheetHeader,
  SheetTitle,
} from '@/components/ui/sheet'
import { Textarea } from '@/components/ui/textarea'
import { useQuota } from '@/hooks/use-quota'
import { handleServerError } from '@/lib/handle-server-error'

import { getUser, groupsQueryOptions, updateUser, usersQueryKey } from '../api'
import {
  USER_FORM_DEFAULTS,
  formValuesToPatch,
  getUserFormSchema,
  userToFormValues,
  type UserFormValues,
} from '../lib/user-form'
import type { AdminUser } from '../types'
import { useUsers } from './users-provider'

interface UsersEditDrawerProps {
  user: AdminUser | null
}

export function UsersEditDrawer({ user }: UsersEditDrawerProps) {
  const { t } = useTranslation()
  const { setDialog, setQuotaUser, refresh } = useUsers()
  const { formatQuota } = useQuota()
  const open = user !== null
  const userId = user?.id

  const schema = useMemo(() => getUserFormSchema(t), [t])
  const form = useForm<UserFormValues>({
    resolver: zodResolver(schema),
    defaultValues: USER_FORM_DEFAULTS,
  })

  const { data: groups } = useQuery(groupsQueryOptions)

  // Always edit the live row: the one in the table may be minutes old.
  const fresh = useQuery({
    queryKey: [...usersQueryKey, 'one', userId],
    queryFn: () => getUser(userId as number),
    enabled: open && userId !== undefined,
    staleTime: 0,
    gcTime: 0,
  })

  // Fill the form once per opened user; a later refetch (after Adjust Quota)
  // refreshes the balance shown but must not wipe what has been typed.
  const filledFor = useRef<number | null>(null)
  useEffect(() => {
    if (!open) {
      filledFor.current = null
      return
    }
    if (fresh.data && filledFor.current !== fresh.data.id) {
      filledFor.current = fresh.data.id
      form.reset(userToFormValues(fresh.data))
    }
  }, [open, fresh.data, form])

  const save = useMutation({
    mutationFn: (values: UserFormValues) =>
      updateUser(userId as number, () => formValuesToPatch(values)),
    onSuccess: () => {
      toast.success(t('User updated successfully'))
      setDialog(null)
      refresh()
    },
    onError: (error) => handleServerError(error, t('Failed to update user')),
  })

  const currentGroup = fresh.data?.group
  const groupOptions = useMemo(() => {
    const names = new Set(groups ?? [])
    if (currentGroup) names.add(currentGroup)
    return [...names].map((name) => ({ value: name, label: name }))
  }, [groups, currentGroup])

  const loadFailed = fresh.isError
  const ready = fresh.data !== undefined

  return (
    <Sheet
      open={open}
      onOpenChange={(next) => {
        if (!next) {
          setDialog(null)
          form.reset(USER_FORM_DEFAULTS)
        }
      }}
    >
      <SheetContent className={sideDrawerContentClassName('sm:max-w-[560px]')}>
        <SheetHeader className={sideDrawerHeaderClassName()}>
          <SheetTitle>{t('Edit User')}</SheetTitle>
          <SheetDescription>
            {t('Update the profile, group and remark of this user.')}
          </SheetDescription>
        </SheetHeader>
        <Form {...form}>
          <form
            id='user-edit-form'
            onSubmit={form.handleSubmit((values) => save.mutate(values))}
            className={sideDrawerFormClassName()}
            aria-busy={fresh.isLoading}
          >
            {loadFailed && (
              <p role='alert' className='text-destructive text-sm'>
                {t('Failed to load')}
              </p>
            )}
            <fieldset disabled={!ready || save.isPending} className='contents'>
              <SideDrawerSection>
                <h3 className='text-sm font-medium'>
                  {t('Basic Information')}
                </h3>
                <FormField
                  control={form.control}
                  name='username'
                  render={({ field }) => (
                    <FormItem>
                      <FormLabel>{t('Username')}</FormLabel>
                      <FormControl>
                        <Input {...field} disabled />
                      </FormControl>
                      <FormDescription>
                        {t('The username comes from the sign-in provider.')}
                      </FormDescription>
                    </FormItem>
                  )}
                />
                <FormField
                  control={form.control}
                  name='display_name'
                  render={({ field }) => (
                    <FormItem>
                      <FormLabel>{t('Display Name')}</FormLabel>
                      <FormControl>
                        <Input
                          {...field}
                          placeholder={t('Enter display name')}
                        />
                      </FormControl>
                      <FormDescription>
                        {t('Leave empty to use username')}
                      </FormDescription>
                      <FormMessage />
                    </FormItem>
                  )}
                />
              </SideDrawerSection>

              <SideDrawerSection>
                <h3 className='text-sm font-medium'>{t('Group & Quota')}</h3>
                <FormField
                  control={form.control}
                  name='group'
                  render={({ field }) => (
                    <FormItem>
                      <FormLabel>{t('Group')}</FormLabel>
                      <FormControl>
                        <Combobox
                          options={groupOptions}
                          value={field.value}
                          onValueChange={(value) => field.onChange(value ?? '')}
                          className='w-full'
                          placeholder={t('Select a group')}
                        />
                      </FormControl>
                      <FormMessage />
                    </FormItem>
                  )}
                />
                <div className='space-y-2'>
                  <p className='text-sm leading-none font-medium'>
                    {t('Available Balance')}
                  </p>
                  <div className='flex gap-2'>
                    <Input
                      readOnly
                      aria-label={t('Available Balance')}
                      value={ready ? formatQuota(fresh.data?.quota) : ''}
                      className='flex-1'
                    />
                    <Button
                      type='button'
                      variant='outline'
                      disabled={!ready}
                      onClick={() => user && setQuotaUser(user)}
                    >
                      <Coins className='mr-1 h-4 w-4' />
                      {t('Adjust Quota')}
                    </Button>
                  </div>
                </div>
                <FormField
                  control={form.control}
                  name='remark'
                  render={({ field }) => (
                    <FormItem>
                      <FormLabel>{t('Remark')}</FormLabel>
                      <FormControl>
                        <Textarea
                          {...field}
                          placeholder={t('Admin notes (only visible to admins)')}
                          rows={3}
                        />
                      </FormControl>
                      <FormMessage />
                    </FormItem>
                  )}
                />
              </SideDrawerSection>
            </fieldset>
          </form>
        </Form>
        <SheetFooter className={sideDrawerFooterClassName()}>
          <SheetClose render={<Button variant='outline' />}>
            {t('Close')}
          </SheetClose>
          <Button
            form='user-edit-form'
            type='submit'
            disabled={!ready || save.isPending}
          >
            {save.isPending ? t('Saving...') : t('Save changes')}
          </Button>
        </SheetFooter>
      </SheetContent>
    </Sheet>
  )
}
