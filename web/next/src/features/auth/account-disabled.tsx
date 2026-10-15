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
import { Ban } from 'lucide-react'
import { useTranslation } from 'react-i18next'

import { Badge } from '@/components/ui/badge'
import { Button, buttonVariants } from '@/components/ui/button'

import { AuthStatusCard } from './components/auth-card'
import { AuthLayout } from './components/auth-layout'
import { navigation } from './lib/navigate'

const SUPPORT_MAILTO = 'mailto:support@lurus.cn'
const LOGOUT_URL = '/api/v2/auth/zita-logout'

/**
 * Terminal page reached when the bootstrap call answers 403 for a disabled
 * account. The visitor holds a valid platform identity but their account here
 * is suspended: sending them back through /login would loop because the
 * bootstrap rejects again on every retry, so the state is stated plainly with
 * a contact path.
 *
 * Deliberately outside the app shell: a suspended account must not render
 * navigation or the tenant switcher.
 */
export function AccountDisabledPage() {
  const { t } = useTranslation()
  return (
    <AuthLayout>
      <AuthStatusCard
        testId='account-disabled'
        icon={Ban}
        tone='danger'
        title={
          <span className='flex flex-col items-center gap-2'>
            <Badge variant='destructive'>{t('Account suspended')}</Badge>
            <span>{t('Account disabled')}</span>
          </span>
        }
        actions={
          <>
            <a href={SUPPORT_MAILTO} className={buttonVariants()}>
              {t('Contact administrator')}
            </a>
            <Button
              variant='outline'
              onClick={() => navigation.assign(LOGOUT_URL)}
            >
              {t('Switch account')}
            </Button>
          </>
        }
      >
        {t(
          'Your account is currently disabled and cannot access this service. To restore access, please contact the system administrator.'
        )}
      </AuthStatusCard>
    </AuthLayout>
  )
}
