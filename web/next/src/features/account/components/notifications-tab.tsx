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
import { Bell, Loader2, Mail, Server, Webhook } from 'lucide-react'
import { useEffect, useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { PasswordInput } from '@/components/password-input'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Switch } from '@/components/ui/switch'
import { TitledCard } from '@/components/ui/titled-card'
import { ToggleGroup, ToggleGroupItem } from '@/components/ui/toggle-group'
import { useQuota } from '@/hooks/use-quota'
import { useStatus } from '@/hooks/use-status'
import { toApiError } from '@/lib/api'
import { currentUserQueryKey } from '@/lib/user'

import { accountKeys, saveUserSettings } from '../api'
import {
  checkSettings,
  formFromSettings,
  parseUserSettings,
  sameForm,
  type SettingsForm,
} from '../lib/settings'
import { NOTIFY_TYPES, type AccountProfile, type NotifyType } from '../types'

const NOTIFY_ICONS: Record<NotifyType, typeof Mail> = {
  email: Mail,
  webhook: Webhook,
  bark: Bell,
  gotify: Server,
}

const NOTIFY_LABELS: Record<NotifyType, string> = {
  email: 'Email',
  webhook: 'Webhook',
  bark: 'Bark',
  gotify: 'Gotify',
}

function Field(props: {
  id: string
  label: string
  hint?: string
  children: React.ReactNode
}) {
  return (
    <div className='space-y-1.5'>
      <Label htmlFor={props.id}>{props.label}</Label>
      {props.children}
      {props.hint && (
        <p className='text-muted-foreground text-xs'>{props.hint}</p>
      )}
    </div>
  )
}

export function NotificationsTab(props: {
  profile: AccountProfile | undefined
}) {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const { ready, symbol, isTokens, toDisplayAmount, fromDisplayAmount } =
    useQuota()
  const statusState = useStatus()

  const stored = useMemo(
    () =>
      ready && props.profile
        ? formFromSettings(
            parseUserSettings(props.profile.setting),
            toDisplayAmount
          )
        : null,
    [ready, props.profile, toDisplayAmount]
  )
  const [form, setForm] = useState<SettingsForm | null>(stored)

  // Adopt the server copy whenever it changes (first load, after a save).
  useEffect(() => {
    setForm(stored)
  }, [stored])

  const save = useMutation({
    mutationFn: async (next: SettingsForm) => {
      const checked = checkSettings(next, fromDisplayAmount, t)
      if (!checked.ok) throw new Error(checked.message)
      await saveUserSettings(checked.body)
    },
    onSuccess: async () => {
      toast.success(t('Settings updated successfully'))
      await Promise.all([
        queryClient.invalidateQueries({ queryKey: accountKeys.profile }),
        queryClient.invalidateQueries({ queryKey: currentUserQueryKey }),
      ])
    },
    onError: (error) => {
      toast.error(toApiError(error).message || t('Failed to update settings'))
    },
  })

  if (!ready && statusState.error) {
    return (
      <p role='alert' className='text-destructive py-8 text-sm'>
        {t(
          'The currency settings could not be loaded, so amounts cannot be shown.'
        )}
      </p>
    )
  }

  if (!form || !stored) {
    return (
      <div
        role='status'
        className='text-muted-foreground flex items-center gap-2 py-8 text-sm'
      >
        <Loader2 className='size-4 animate-spin' />
        {t('Loading...')}
      </div>
    )
  }

  const patch = <K extends keyof SettingsForm>(
    key: K,
    value: SettingsForm[K]
  ) => setForm((prev) => (prev ? { ...prev, [key]: value } : prev))
  const dirty = !sameForm(form, stored)

  return (
    <form
      className='space-y-4 sm:space-y-6'
      onSubmit={(event) => {
        event.preventDefault()
        save.mutate(form)
      }}
    >
      <TitledCard
        title={t('Quota alerts')}
        description={t('Choose how you are told when your balance runs low')}
        icon={<Bell className='size-4' />}
        iconTone='warning'
        disableHoverEffect
      >
        <div className='space-y-4 sm:space-y-6'>
          <div className='space-y-2.5'>
            <Label id='notify-method-label'>{t('Notification Method')}</Label>
            <ToggleGroup
              value={[form.notifyType]}
              onValueChange={(value) => {
                const next = value.find((item) => item !== form.notifyType)
                if (next) patch('notifyType', next as NotifyType)
              }}
              aria-labelledby='notify-method-label'
              variant='outline'
              size='lg'
              spacing={2}
              className='grid w-full grid-cols-2 gap-2 sm:grid-cols-4 sm:gap-3'
            >
              {NOTIFY_TYPES.map((type) => {
                const Icon = NOTIFY_ICONS[type]
                return (
                  <ToggleGroupItem
                    key={type}
                    value={type}
                    className='h-auto min-h-14 w-full flex-col gap-1.5 px-3 py-3 sm:min-h-16'
                  >
                    <Icon className='size-4 sm:size-5' />
                    <span className='max-w-full truncate text-xs font-medium sm:text-sm'>
                      {t(NOTIFY_LABELS[type])}
                    </span>
                  </ToggleGroupItem>
                )
              })}
            </ToggleGroup>
          </div>

          <Field
            id='notify-threshold'
            label={
              isTokens
                ? t('Quota Warning Threshold')
                : t('Quota Warning Threshold ({{symbol}})', { symbol })
            }
            hint={t('Get notified when your balance falls below this value')}
          >
            <Input
              id='notify-threshold'
              type='number'
              min='0'
              step='any'
              inputMode='decimal'
              className='h-9'
              value={form.threshold}
              onChange={(e) => patch('threshold', e.target.value)}
              placeholder={t('Enter threshold')}
            />
          </Field>

          {form.notifyType === 'email' && (
            <Field
              id='notify-email'
              label={t('Notification Email')}
              hint={t('Leave empty to use your account email')}
            >
              <Input
                id='notify-email'
                type='email'
                className='h-9'
                value={form.notificationEmail}
                onChange={(e) => patch('notificationEmail', e.target.value)}
              />
            </Field>
          )}

          {form.notifyType === 'webhook' && (
            <>
              <Field id='notify-webhook-url' label={t('Webhook URL')}>
                <Input
                  id='notify-webhook-url'
                  type='url'
                  className='h-9'
                  value={form.webhookUrl}
                  onChange={(e) => patch('webhookUrl', e.target.value)}
                  placeholder={t('https://example.com/webhook')}
                />
              </Field>
              <Field id='notify-webhook-secret' label={t('Webhook Secret')}>
                <PasswordInput
                  id='notify-webhook-secret'
                  value={form.webhookSecret}
                  onChange={(e) => patch('webhookSecret', e.target.value)}
                  placeholder={t('Enter secret key')}
                />
              </Field>
            </>
          )}

          {form.notifyType === 'bark' && (
            <Field
              id='notify-bark-url'
              label={t('Bark Push URL')}
              hint={`${t('Template variables:')} {{title}}, {{content}}`}
            >
              <Input
                id='notify-bark-url'
                type='url'
                className='h-9'
                value={form.barkUrl}
                onChange={(e) => patch('barkUrl', e.target.value)}
                placeholder={t(
                  'https://api.day.app/yourkey/{{title}}/{{content}}'
                )}
              />
            </Field>
          )}

          {form.notifyType === 'gotify' && (
            <>
              <Field
                id='notify-gotify-url'
                label={t('Gotify Server URL')}
                hint={t('Enter the full URL of your Gotify server')}
              >
                <Input
                  id='notify-gotify-url'
                  type='url'
                  className='h-9'
                  value={form.gotifyUrl}
                  onChange={(e) => patch('gotifyUrl', e.target.value)}
                  placeholder={t('https://gotify.example.com')}
                />
              </Field>
              <Field
                id='notify-gotify-token'
                label={t('Gotify Application Token')}
                hint={t('Token obtained from your Gotify application')}
              >
                <PasswordInput
                  id='notify-gotify-token'
                  value={form.gotifyToken}
                  onChange={(e) => patch('gotifyToken', e.target.value)}
                  placeholder={t('Enter application token')}
                />
              </Field>
              <Field
                id='notify-gotify-priority'
                label={t('Message Priority')}
                hint={t(
                  'Priority level from 0 (lowest) to 10 (highest), default is 5'
                )}
              >
                <Input
                  id='notify-gotify-priority'
                  type='number'
                  min='0'
                  max='10'
                  className='h-9'
                  value={form.gotifyPriority}
                  onChange={(e) => patch('gotifyPriority', e.target.value)}
                  placeholder='5'
                />
              </Field>
            </>
          )}
        </div>
      </TitledCard>

      <TitledCard
        title={t('Preferences')}
        description={t('Configure your account behavior preferences')}
        disableHoverEffect
      >
        <div className='space-y-3'>
          <div className='flex items-start justify-between gap-3 rounded-lg border p-3 sm:items-center sm:p-4'>
            <div className='space-y-0.5'>
              <Label htmlFor='pref-unpriced'>
                {t('Accept Unpriced Models')}
              </Label>
              <p className='text-muted-foreground text-xs sm:text-sm'>
                {t('Allow calls to models that have no price configured')}
              </p>
            </div>
            <Switch
              id='pref-unpriced'
              className='shrink-0'
              checked={form.acceptUnpriced}
              onCheckedChange={(checked) => patch('acceptUnpriced', checked)}
            />
          </div>
          <div className='flex items-start justify-between gap-3 rounded-lg border p-3 sm:items-center sm:p-4'>
            <div className='space-y-0.5'>
              <Label htmlFor='pref-record-ip'>{t('Record IP Address')}</Label>
              <p className='text-muted-foreground text-xs sm:text-sm'>
                {t('Log IP address for usage and error logs')}
              </p>
            </div>
            <Switch
              id='pref-record-ip'
              className='shrink-0'
              checked={form.recordIpLog}
              onCheckedChange={(checked) => patch('recordIpLog', checked)}
            />
          </div>
        </div>
      </TitledCard>

      <div className='flex justify-end gap-2'>
        <Button
          type='button'
          variant='outline'
          disabled={!dirty || save.isPending}
          onClick={() => setForm(stored)}
        >
          {t('Reset')}
        </Button>
        <Button type='submit' disabled={!dirty || save.isPending}>
          {save.isPending && <Loader2 className='size-4 animate-spin' />}
          {save.isPending ? t('Saving...') : t('Save Settings')}
        </Button>
      </div>
    </form>
  )
}
