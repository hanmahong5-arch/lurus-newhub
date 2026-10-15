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
import { redirectToLogin } from '@/lib/auth'

/** 403: signed in, but not allowed to read the pool overview. */
export function PoolsForbidden() {
  const { t } = useTranslation()
  return (
    <div data-testid='pools-forbidden'>
      <ErrorState
        icon={ShieldAlert}
        title={t('Admin access required')}
        description={t('You do not have permission to view model pools.')}
      />
    </div>
  )
}

/** 401: the session lapsed; nothing on this page can be read until sign-in. */
export function PoolsSignedOut() {
  const { t } = useTranslation()
  return (
    <div data-testid='pools-signed-out'>
      <ErrorState
        icon={LogIn}
        title={t('Your session has expired')}
        description={t(
          'The server no longer recognises this session, so nothing on this page can be read. Sign in again to continue.'
        )}
        action={
          <Button size='sm' onClick={() => redirectToLogin()}>
            {t('Sign in again')}
          </Button>
        }
      />
    </div>
  )
}

/** Anything else: the overview is unknown, which is not "no pools exist". */
export function PoolsError(props: { message: string; onRetry: () => void }) {
  const { t } = useTranslation()
  const lead = t(
    'The model-pools endpoint did not answer. This is not a report that no pools exist — the overview simply could not be read.'
  )
  const detail = props.message.trim()
  return (
    <div data-testid='pools-error'>
      <ErrorState
        title={t('Model pools unknown')}
        description={detail ? `${lead} ${detail}` : lead}
        onRetry={props.onRetry}
      />
    </div>
  )
}
