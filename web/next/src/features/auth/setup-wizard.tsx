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
  Check,
  CheckCircle2,
  Database,
  TriangleAlert,
  UserRound,
  X,
} from 'lucide-react'
import { useCallback, useEffect, useRef, useState } from 'react'
import type { TFunction } from 'i18next'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card, CardContent } from '@/components/ui/card'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { http, type ApiEnvelope } from '@/lib/api'
import { cn } from '@/lib/utils'

import { AuthStatusCard } from './components/auth-card'
import { AuthLayout } from './components/auth-layout'
import { navigation } from './lib/navigate'
import {
  buildSetupPayload,
  USERNAME_MAX_CHARS,
  usernameProblem,
  type SetupStatus,
  type UsageMode,
} from './lib/setup-rules'

/** Delay between "initialized" and the reload into the finished system. */
const RELOAD_DELAY_MS = 1500

const STEP_COUNT = 4
const LAST_STEP = STEP_COUNT - 1

interface ModeFeature {
  label: string
  included: boolean
}

function modeFeatures(mode: UsageMode, t: TFunction): ModeFeature[] {
  switch (mode) {
    case 'external':
      return [
        { label: t('User registration and management'), included: true },
        { label: t('Quota and billing'), included: true },
        { label: t('Multiple API keys'), included: true },
        { label: t('Usage statistics'), included: true },
      ]
    case 'self':
      return [
        { label: t('Simplified interface'), included: true },
        { label: t('No user registration'), included: false },
        { label: t('No pricing setup'), included: false },
        { label: t('Unified API key'), included: true },
      ]
    case 'demo':
      return [
        { label: t('Demo data'), included: true },
        { label: t('Limited permissions'), included: true },
        { label: t('Quick exploration'), included: true },
      ]
  }
}

function ModeFeatures(props: { mode: UsageMode }) {
  const { t } = useTranslation()
  return (
    <span className='mt-2 flex flex-wrap gap-1'>
      {modeFeatures(props.mode, t).map(({ label, included }) => (
        <Badge
          key={label}
          variant={included ? 'secondary' : 'outline'}
          data-included={included ? 'true' : 'false'}
          className={cn(!included && 'text-muted-foreground')}
        >
          {included ? <Check aria-hidden='true' /> : <X aria-hidden='true' />}
          {label}
        </Badge>
      ))}
    </span>
  )
}

function DatabaseStep(props: { status: SetupStatus }) {
  const { t } = useTranslation()
  const type = props.status.database_type
  if (type === 'sqlite') {
    return (
      <Alert data-testid='setup-db-sqlite'>
        <TriangleAlert aria-hidden='true' />
        <AlertTitle>{t('Database warning')}</AlertTitle>
        <AlertDescription>
          <p>
            {t(
              'You are using a SQLite database. If you run in a container, make sure the database file is mapped to persistent storage, otherwise all data is lost when the container restarts.'
            )}
          </p>
          <p className='mt-1 font-medium'>
            {t(
              'Use PostgreSQL in production, or make sure the SQLite file is mapped to persistent storage on the host.'
            )}
          </p>
        </AlertDescription>
      </Alert>
    )
  }
  if (type === 'postgres') {
    return (
      <Alert data-testid='setup-db-postgres'>
        <Database aria-hidden='true' />
        <AlertTitle>{t('Database information')}</AlertTitle>
        <AlertDescription>
          {t(
            'You are using a PostgreSQL database, an open-source relational database with strong reliability and data integrity, suitable for production.'
          )}
        </AlertDescription>
      </Alert>
    )
  }
  // The server did not name a database: say nothing rather than guess.
  return (
    <p className='text-muted-foreground text-sm' data-testid='setup-db-unknown'>
      {t('The database type was not reported.')}
    </p>
  )
}

function AdminStep(props: {
  status: SetupStatus
  username: string
  onUsername: (value: string) => void
  invalid: boolean
}) {
  const { t } = useTranslation()
  if (props.status.root_init) {
    return (
      <Alert data-testid='setup-root-initialized'>
        <UserRound aria-hidden='true' />
        <AlertDescription>
          {t(
            'The admin account has already been initialized, please continue with the other settings.'
          )}
        </AlertDescription>
      </Alert>
    )
  }
  return (
    <div className='flex flex-col gap-3'>
      <div className='flex flex-col gap-1.5'>
        <Label htmlFor='setup-username'>{t('Username')}</Label>
        <Input
          id='setup-username'
          data-testid='setup-username'
          autoComplete='off'
          value={props.username}
          aria-invalid={props.invalid || undefined}
          placeholder={t('Please enter the admin username')}
          onChange={(event) => props.onUsername(event.target.value)}
        />
        <p className='text-muted-foreground text-xs'>
          {t(
            'Accounts and passwords are managed by the identity platform. The administrator signs in through single sign-on; this name is only the local account label (1-{{max}} characters).',
            { max: USERNAME_MAX_CHARS }
          )}
        </p>
      </div>
    </div>
  )
}

function UsageModeStep(props: {
  mode: UsageMode
  onMode: (mode: UsageMode) => void
}) {
  const { t } = useTranslation()
  const options: Array<{ mode: UsageMode; title: string; hint: string }> = [
    {
      mode: 'external',
      title: t('Default mode'),
      hint: t('For serving multiple users'),
    },
    {
      mode: 'self',
      title: t('Self-use mode'),
      hint: t('For personal use, no need to set model prices'),
    },
    {
      mode: 'demo',
      title: t('Demo site mode'),
      hint: t('For showing off system features with basic demonstrations'),
    },
  ]
  return (
    <div className='flex flex-col gap-2'>
      <div
        role='radiogroup'
        aria-label={t('Usage mode')}
        className='grid gap-3 md:grid-cols-3'
      >
        {options.map((option) => {
          const checked = props.mode === option.mode
          return (
            <button
              key={option.mode}
              type='button'
              role='radio'
              aria-checked={checked}
              data-testid={`setup-mode-${option.mode}`}
              onClick={() => props.onMode(option.mode)}
              className={cn(
                'focus-visible:ring-ring/50 flex flex-col rounded-lg border p-3 text-start transition-colors outline-none focus-visible:ring-3',
                checked
                  ? 'border-primary bg-primary/5'
                  : 'hover:bg-muted/50 border-border'
              )}
            >
              <span className='text-sm font-medium'>{option.title}</span>
              <span className='text-muted-foreground mt-1 text-xs'>
                {option.hint}
              </span>
              <ModeFeatures mode={option.mode} />
            </button>
          )
        })}
      </div>
      <p className='text-muted-foreground text-xs'>
        {t('You can change the mode anytime in System Settings')}
      </p>
    </div>
  )
}

function CompleteStep(props: {
  status: SetupStatus
  username: string
  mode: UsageMode
}) {
  const { t } = useTranslation()
  const dbNames: Record<string, string> = {
    sqlite: 'SQLite',
    postgres: 'PostgreSQL',
  }
  const dbLabel = dbNames[props.status.database_type] ?? t('Unknown')
  const modeNames: Record<UsageMode, string> = {
    external: t('Default mode'),
    self: t('Self-use mode'),
    demo: t('Demo site mode'),
  }
  const modeLabel = modeNames[props.mode]
  const adminLabel = props.status.root_init
    ? t('Initialized')
    : props.username.trim() || t('Not set')

  const rows: Array<[string, string, string]> = [
    ['database', t('Database type'), dbLabel],
    ['admin', t('Admin account'), adminLabel],
    ['mode', t('Usage mode'), modeLabel],
  ]
  return (
    <div className='flex flex-col items-center gap-4 text-center'>
      <span className='bg-primary/10 text-primary flex size-11 items-center justify-center rounded-full'>
        <CheckCircle2 aria-hidden='true' className='size-5' />
      </span>
      <div>
        <h2 className='text-base font-semibold'>
          {t('Ready to complete initialization')}
        </h2>
        <p className='text-muted-foreground mt-1 text-sm'>
          {t(
            'Review the settings below, then choose "Initialize system" to apply them.'
          )}
        </p>
      </div>
      <dl
        className='grid w-full max-w-md grid-cols-[auto_1fr] gap-x-6 gap-y-2 text-start text-sm'
        data-testid='setup-summary'
      >
        {rows.map(([key, label, value]) => (
          <div key={key} className='contents'>
            <dt className='text-muted-foreground'>{label}</dt>
            <dd className='font-medium' data-summary-key={key}>
              {value}
            </dd>
          </div>
        ))}
      </dl>
    </div>
  )
}

type Load =
  | { kind: 'loading' }
  | { kind: 'ready'; status: SetupStatus }
  | { kind: 'done' }
  | { kind: 'failed' }

export function SetupWizard() {
  const { t, i18n } = useTranslation()
  const [load, setLoad] = useState<Load>({ kind: 'loading' })
  const [step, setStep] = useState(0)
  const [username, setUsername] = useState('')
  const [usageMode, setUsageMode] = useState<UsageMode>('external')
  const [usernameInvalid, setUsernameInvalid] = useState(false)
  const [submitting, setSubmitting] = useState(false)
  // Set once the server accepted the initialization: the page is about to
  // reload and must not take a second submit in the meantime.
  const accepted = useRef(false)
  const reloadTimer = useRef<ReturnType<typeof setTimeout> | undefined>(
    undefined
  )

  // Loads GET /api/setup. Resolves to what happened so the caller can report
  // a failure; it depends on nothing that changes (a language switch in the
  // middle of the wizard must not reset it).
  const fetchStatus = useCallback(async (): Promise<boolean> => {
    setLoad({ kind: 'loading' })
    try {
      const res = await http.get<ApiEnvelope<SetupStatus>>('/api/setup', {
        skipAuthRedirect: true,
      })
      const { success, data } = res.data
      if (!success || !data) {
        setLoad({ kind: 'failed' })
        return false
      }
      // Already initialized: nothing to do here.
      if (data.status) {
        setLoad({ kind: 'done' })
        navigation.assign('/')
        return true
      }
      setStep(0)
      setLoad({ kind: 'ready', status: data })
      return true
    } catch {
      setLoad({ kind: 'failed' })
      return false
    }
  }, [])

  const loadStatus = useCallback(async () => {
    if (!(await fetchStatus())) {
      toast.error(i18n.t('Failed to get initialization status'))
    }
  }, [fetchStatus, i18n])

  useEffect(() => {
    void loadStatus()
    return () => {
      if (reloadTimer.current) clearTimeout(reloadTimer.current)
    }
    // Once per visit; loadStatus only changes if the i18n instance does.
  }, [loadStatus])

  if (load.kind === 'loading' || load.kind === 'done') {
    return (
      <AuthLayout>
        <AuthStatusCard
          busy
          testId='setup-progress'
          title={t('Loading...')}
        />
      </AuthLayout>
    )
  }

  if (load.kind === 'failed') {
    return (
      <AuthLayout>
        <AuthStatusCard
          testId='setup-load-failed'
          icon={TriangleAlert}
          tone='danger'
          title={t('Failed to get initialization status')}
          actions={
            <Button variant='outline' onClick={() => void loadStatus()}>
              {t('Retry')}
            </Button>
          }
        />
      </AuthLayout>
    )
  }

  const status = load.status

  /** Validate what step `index` collects; toast the first problem. */
  const stepProblem = (index: number): string | null => {
    if (index === 1 && !status.root_init) {
      const problem = usernameProblem(username)
      if (problem === 'required') return t('Please enter the admin username')
      if (problem === 'too-long') {
        return t('The username must be 1-{{max}} characters long', {
          max: USERNAME_MAX_CHARS,
        })
      }
    }
    return null
  }

  const next = () => {
    const problem = stepProblem(step)
    if (problem) {
      setUsernameInvalid(true)
      toast.error(problem)
      return
    }
    setUsernameInvalid(false)
    setStep((current) => Math.min(current + 1, LAST_STEP))
  }

  const prev = () => setStep((current) => Math.max(current - 1, 0))

  const submit = async () => {
    // A run the server already accepted must not be started twice: a second
    // POST would be a second attempt to create the root administrator.
    if (submitting || accepted.current) return
    const problem = stepProblem(1)
    if (problem) {
      toast.error(problem)
      setStep(1)
      return
    }
    setSubmitting(true)
    try {
      const res = await http.post<ApiEnvelope<unknown>>(
        '/api/setup',
        buildSetupPayload({
          rootInit: status.root_init,
          username,
          usageMode,
        }),
        { skipAuthRedirect: true }
      )
      if (res.data.success) {
        accepted.current = true
        toast.success(t('System initialized, redirecting...'))
        // Leave `submitting` set: the button stays busy until the reload.
        reloadTimer.current = setTimeout(
          () => navigation.reload(),
          RELOAD_DELAY_MS
        )
        return
      }
      toast.error(res.data.message || t('Initialization failed, please retry'))
    } catch {
      toast.error(t('System initialization failed, please try again'))
    }
    setSubmitting(false)
  }

  const stepTitles = [
    { title: t('Database check'), hint: t('Verify the database connection') },
    { title: t('Admin account'), hint: t('Set the administrator sign-in') },
    { title: t('Usage mode'), hint: t('Choose how the system runs') },
    {
      title: t('Complete initialization'),
      hint: t('Confirm and finish setup'),
    },
  ]

  return (
    <AuthLayout wide>
      <Card className='shadow-sm' data-testid='setup-wizard'>
        <CardContent className='flex flex-col gap-6 px-4 py-4 sm:px-6'>
          <div>
            <h1 className='text-lg font-semibold'>{t('System initialization')}</h1>
            <p className='text-muted-foreground text-sm'>
              {t('Welcome! Complete the settings below to start using the system.')}
            </p>
          </div>

          <ol
            className='grid grid-cols-2 gap-3 sm:grid-cols-4'
            data-testid='setup-steps'
            data-current={step}
          >
            {stepTitles.map((item, index) => (
              <li
                key={item.title}
                aria-current={index === step ? 'step' : undefined}
                className={cn(
                  'flex flex-col gap-0.5 border-t-2 pt-2',
                  index <= step ? 'border-primary' : 'border-border'
                )}
              >
                <span
                  className={cn(
                    'text-sm font-medium',
                    index > step && 'text-muted-foreground'
                  )}
                >
                  {item.title}
                </span>
                <span className='text-muted-foreground hidden text-xs sm:block'>
                  {item.hint}
                </span>
              </li>
            ))}
          </ol>

          <div className='min-h-32'>
            {step === 0 && <DatabaseStep status={status} />}
            {step === 1 && (
              <AdminStep
                status={status}
                username={username}
                invalid={usernameInvalid}
                onUsername={(value) => {
                  setUsername(value)
                  setUsernameInvalid(false)
                }}
              />
            )}
            {step === 2 && (
              <UsageModeStep mode={usageMode} onMode={setUsageMode} />
            )}
            {step === LAST_STEP && (
              <CompleteStep
                status={status}
                username={username}
                mode={usageMode}
              />
            )}
          </div>

          <div className='flex items-center justify-between gap-2'>
            {step > 0 ? (
              <Button variant='outline' onClick={prev} disabled={submitting}>
                {t('Previous step')}
              </Button>
            ) : (
              <span />
            )}
            {step < LAST_STEP ? (
              <Button onClick={next}>{t('Next step')}</Button>
            ) : (
              <Button
                onClick={() => void submit()}
                disabled={submitting}
                data-loading={submitting ? 'true' : 'false'}
              >
                <CheckCircle2 aria-hidden='true' />
                {t('Initialize system')}
              </Button>
            )}
          </div>
        </CardContent>
      </Card>
    </AuthLayout>
  )
}
