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
import { useMutation } from '@tanstack/react-query'
import { useEffect, useMemo } from 'react'
import { useForm } from 'react-hook-form'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { DateTimePicker } from '@/components/datetime-picker'
import {
  SideDrawerSection,
  sideDrawerContentClassName,
  sideDrawerFooterClassName,
  sideDrawerFormClassName,
  sideDrawerHeaderClassName,
} from '@/components/drawer-layout'
import { Button } from '@/components/ui/button'
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
import { useQuota } from '@/hooks/use-quota'
import { handleServerError } from '@/lib/handle-server-error'
import { addTimeToDate } from '@/lib/time'

import { createRedemptions, updateRedemption } from '../api'
import { COUNT_MAX } from '../constants'
import {
  REDEMPTION_FORM_DEFAULTS,
  dateToExpiry,
  formValuesToBody,
  getRedemptionFormSchema,
  redemptionToFormValues,
  type RedemptionFormValues,
} from '../lib/redemption-form'
import type { Redemption } from '../types'
import { useRedemptions } from './redemptions-provider'

interface RedemptionsMutateDrawerProps {
  open: boolean
  /** Set to edit that code; unset to create a batch. */
  redemption?: Redemption
}

const EXPIRY_PRESETS: { label: string; months: number; days: number }[] = [
  { label: 'Never', months: 0, days: 0 },
  { label: '1M', months: 1, days: 0 },
  { label: '1W', months: 0, days: 7 },
  { label: '1 Day', months: 0, days: 1 },
]

export function RedemptionsMutateDrawer({
  open,
  redemption,
}: RedemptionsMutateDrawerProps) {
  const { t } = useTranslation()
  const { setDialog, refresh } = useRedemptions()
  const { ready, symbol, isTokens, fromDisplayAmount, toDisplayAmount } =
    useQuota()
  const isUpdate = redemption !== undefined
  const unit = isTokens ? t('Tokens') : symbol

  const schema = useMemo(() => getRedemptionFormSchema(t), [t])
  const form = useForm<RedemptionFormValues>({
    resolver: zodResolver(schema),
    defaultValues: REDEMPTION_FORM_DEFAULTS,
  })

  // Fill the form each time the drawer opens, and again once the currency
  // settings arrive (an edit cannot show its amount before that).
  useEffect(() => {
    if (!open) return
    form.reset(
      redemption
        ? redemptionToFormValues(redemption, toDisplayAmount)
        : REDEMPTION_FORM_DEFAULTS
    )
  }, [open, redemption, toDisplayAmount, form])

  const submit = useMutation({
    mutationFn: async (values: RedemptionFormValues) => {
      const quotaTouched = form.getFieldState('quota').isDirty
      if (redemption) {
        const converted = formValuesToBody(values, fromDisplayAmount)
        // An untouched amount is saved exactly as stored, not re-derived.
        let quota = redemption.quota
        if (quotaTouched) {
          if (!converted.ok) throw new Error(t('Enter a valid amount'))
          quota = converted.body.quota
        }
        await updateRedemption({
          id: redemption.id,
          name: values.name.trim(),
          quota,
          expired_time: dateToExpiry(values.expired_time),
        })
        return null
      }
      const converted = formValuesToBody(values, fromDisplayAmount)
      if (!converted.ok) throw new Error(t('Enter a valid amount'))
      const created = await createRedemptions(converted.body)
      return { created, body: converted.body }
    },
    onSuccess: (result) => {
      refresh()
      if (!result) {
        toast.success(t('Redemption code updated successfully'))
        setDialog(null)
        return
      }
      const { created, body } = result
      if (created.count < created.requested) {
        toast.warning(
          t('Only {{count}} of {{requested}} redemption codes were created', {
            count: created.count,
            requested: created.requested,
          })
        )
      } else {
        toast.success(
          created.count > 1
            ? t('Successfully created {{count}} redemption codes', {
                count: created.count,
              })
            : t('Redemption code(s) created successfully')
        )
      }
      setDialog({
        kind: 'created',
        batch: {
          keys: created.codes.map((code) => code.key),
          name: body.name,
          quota: body.quota,
          requested: created.requested,
        },
      })
    },
    onError: (error) =>
      handleServerError(
        error,
        isUpdate
          ? t('Failed to update redemption code')
          : t('Failed to create redemption code')
      ),
  })

  const setExpiry = (months: number, days: number) => {
    form.setValue('expired_time', addTimeToDate(months, days, 0), {
      shouldDirty: true,
    })
  }

  return (
    <Sheet
      open={open}
      onOpenChange={(next) => {
        if (!next && !submit.isPending) setDialog(null)
      }}
    >
      <SheetContent className={sideDrawerContentClassName('sm:max-w-[560px]')}>
        <SheetHeader className={sideDrawerHeaderClassName()}>
          <SheetTitle>
            {isUpdate
              ? t('Update Redemption Code')
              : t('Create Redemption Code')}
          </SheetTitle>
          <SheetDescription>
            {isUpdate
              ? t('Update the redemption code by providing necessary info.')
              : t('Add new redemption code(s) by providing necessary info.')}
          </SheetDescription>
        </SheetHeader>
        <Form {...form}>
          <form
            id='redemption-form'
            onSubmit={form.handleSubmit((values) => submit.mutate(values))}
            className={sideDrawerFormClassName()}
          >
            <fieldset disabled={submit.isPending} className='contents'>
              <SideDrawerSection>
                <FormField
                  control={form.control}
                  name='name'
                  render={({ field }) => (
                    <FormItem>
                      <FormLabel>{t('Name')}</FormLabel>
                      <FormControl>
                        <Input {...field} placeholder={t('Enter a name')} />
                      </FormControl>
                      <FormDescription>
                        {t('Name for this redemption code (1-50 bytes)')}
                      </FormDescription>
                      <FormMessage />
                    </FormItem>
                  )}
                />

                <FormField
                  control={form.control}
                  name='quota'
                  render={({ field }) => (
                    <FormItem>
                      <FormLabel>
                        {unit
                          ? t('Quota ({{currency}})', { currency: unit })
                          : t('Quota')}
                      </FormLabel>
                      <FormControl>
                        <Input
                          {...field}
                          inputMode='decimal'
                          autoComplete='off'
                          disabled={!ready}
                          placeholder={t('Enter quota')}
                        />
                      </FormControl>
                      <FormDescription>
                        {ready
                          ? t('The balance each code adds when redeemed.')
                          : t('The currency settings have not loaded yet.')}
                      </FormDescription>
                      <FormMessage />
                    </FormItem>
                  )}
                />

                <FormField
                  control={form.control}
                  name='expired_time'
                  render={({ field }) => (
                    <FormItem>
                      <FormLabel>{t('Expiration Time')}</FormLabel>
                      <div className='flex flex-col gap-2'>
                        <FormControl>
                          <DateTimePicker
                            value={field.value}
                            onChange={field.onChange}
                            placeholder={t('Never expires')}
                          />
                        </FormControl>
                        <div className='grid grid-cols-4 gap-1.5 sm:flex sm:gap-2'>
                          {EXPIRY_PRESETS.map((preset) => (
                            <Button
                              key={preset.label}
                              type='button'
                              variant='outline'
                              size='sm'
                              onClick={() =>
                                setExpiry(preset.months, preset.days)
                              }
                            >
                              {t(preset.label)}
                            </Button>
                          ))}
                        </div>
                      </div>
                      <FormDescription>
                        {t('Leave empty for never expires')}
                      </FormDescription>
                      <FormMessage />
                    </FormItem>
                  )}
                />

                {!isUpdate && (
                  <FormField
                    control={form.control}
                    name='count'
                    render={({ field }) => (
                      <FormItem>
                        <FormLabel>{t('Quantity')}</FormLabel>
                        <FormControl>
                          <Input
                            {...field}
                            type='number'
                            min={1}
                            max={COUNT_MAX}
                            placeholder={t('Number of codes to create')}
                            onChange={(e) =>
                              field.onChange(
                                e.target.value === ''
                                  ? Number.NaN
                                  : Number.parseInt(e.target.value, 10)
                              )
                            }
                          />
                        </FormControl>
                        <FormDescription>
                          {t(
                            'Create multiple redemption codes at once (1-100)'
                          )}
                        </FormDescription>
                        <FormMessage />
                      </FormItem>
                    )}
                  />
                )}
              </SideDrawerSection>
            </fieldset>
          </form>
        </Form>
        <SheetFooter className={sideDrawerFooterClassName()}>
          <SheetClose render={<Button variant='outline' />}>
            {t('Close')}
          </SheetClose>
          <Button
            form='redemption-form'
            type='submit'
            disabled={submit.isPending || !ready}
          >
            {submit.isPending ? t('Saving...') : t('Save changes')}
          </Button>
        </SheetFooter>
      </SheetContent>
    </Sheet>
  )
}
