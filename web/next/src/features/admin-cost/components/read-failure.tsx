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
import { LogIn, ShieldAlert } from 'lucide-react'
import { useTranslation } from 'react-i18next'

import { ErrorState } from '@/components/error-state'
import { Button } from '@/components/ui/button'
import {
  Empty,
  EmptyContent,
  EmptyDescription,
  EmptyHeader,
  EmptyMedia,
  EmptyTitle,
} from '@/components/ui/empty'
import { buildLoginUrl } from '@/lib/auth'

import { classifyReadFailure, readFailureMessage } from '../lib/read-failure'

export interface ReadFailureProps {
  error: unknown
  onRetry: () => void
  /** Prefix for the data-testid hooks: `<prefix>-error`, `-signed-out`, ... */
  testIdPrefix: string
  forbiddenDescription: string
  errorTitle: string
  errorDescription: string
}

export function ReadFailure(props: ReadFailureProps) {
  const { t } = useTranslation()
  const kind = classifyReadFailure(props.error)

  if (kind === 'forbidden') {
    return (
      <div data-testid={`${props.testIdPrefix}-forbidden`}>
        <Empty className='min-h-[260px]'>
          <EmptyHeader>
            <EmptyMedia variant='icon'>
              <ShieldAlert className='size-6' />
            </EmptyMedia>
            <EmptyTitle>{t('Admin access required')}</EmptyTitle>
            <EmptyDescription>{props.forbiddenDescription}</EmptyDescription>
          </EmptyHeader>
        </Empty>
      </div>
    )
  }

  if (kind === 'signed-out') {
    const here =
      typeof window === 'undefined'
        ? '/'
        : window.location.pathname + window.location.search
    return (
      <div data-testid={`${props.testIdPrefix}-signed-out`}>
        <Empty className='min-h-[260px]'>
          <EmptyHeader>
            <EmptyMedia variant='icon'>
              <LogIn className='size-6' />
            </EmptyMedia>
            <EmptyTitle>{t('Your session has expired')}</EmptyTitle>
            <EmptyDescription>
              {t(
                'The server no longer recognises this session, so nothing on this page can be read. Sign in again to continue.'
              )}
            </EmptyDescription>
          </EmptyHeader>
          <EmptyContent>
            <Button
              size='sm'
              data-testid={`${props.testIdPrefix}-sign-in`}
              render={<a href={buildLoginUrl(here)} />}
              nativeButton={false}
            >
              {t('Sign in again')}
            </Button>
          </EmptyContent>
        </Empty>
      </div>
    )
  }

  const message = readFailureMessage(props.error)
  return (
    <div data-testid={`${props.testIdPrefix}-error`}>
      <ErrorState
        title={props.errorTitle}
        description={
          message
            ? `${props.errorDescription} (${message})`
            : props.errorDescription
        }
        onRetry={props.onRetry}
      />
    </div>
  )
}
