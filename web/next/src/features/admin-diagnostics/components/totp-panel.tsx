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
import { useTranslation } from 'react-i18next'

import { ErrorState } from '@/components/error-state'
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from '@/components/ui/card'
import { Progress } from '@/components/ui/progress'

import { formatPct } from '../lib/format'
import type { TotpStats } from '../types'
import { Stat } from './stat'

export function TotpPanel(props: {
  /** undefined while the read is unavailable. */
  stats: TotpStats | undefined
  failed: boolean
  onRetry: () => void
}) {
  const { t } = useTranslation()
  const stats = props.stats

  return (
    <Card data-testid='diag-security-panel'>
      <CardHeader>
        <CardTitle>{t('Security posture')}</CardTitle>
        <CardDescription>
          {t('Two-factor (TOTP) adoption across all users.')}
        </CardDescription>
      </CardHeader>
      <CardContent className='flex flex-col gap-4'>
        {props.failed || !stats ? (
          // No fabricated 0.0% anywhere: a failed read shows nothing numeric.
          <div data-testid='diag-totp-unavailable'>
            <ErrorState
              className='min-h-[140px]'
              title={t('Could not load TOTP adoption stats')}
              onRetry={props.onRetry}
            />
          </div>
        ) : (
          <>
            <div className='grid grid-cols-2 gap-4 sm:grid-cols-3'>
              <Stat label={t('Enrolled')} testId='diag-totp-enrolled'>
                {stats.enrolled}
              </Stat>
              <Stat label={t('Pending')} testId='diag-totp-pending'>
                {stats.pending}
              </Stat>
              <Stat label={t('Total users')} testId='diag-totp-total'>
                {stats.total_users}
              </Stat>
              <Stat label={t('Adoption')} testId='diag-totp-adoption-pct'>
                {formatPct(stats.adoption_pct)}
              </Stat>
              <Stat
                label={t('Backup codes exhausted')}
                testId='diag-totp-exhausted'
              >
                {stats.backup_codes_exhausted}
              </Stat>
              <Stat
                label={t('No backup codes issued')}
                testId='diag-totp-no-codes'
              >
                {stats.no_codes_issued}
              </Stat>
            </div>
            <Progress
              value={Math.min(100, Math.max(0, stats.adoption_pct))}
              aria-label={t('Adoption')}
            />
            <p
              className='text-muted-foreground text-xs leading-relaxed'
              data-testid='diag-totp-no-factor-note'
            >
              {t(
                'The count of privileged accounts with no second factor is not exposed by this API today.'
              )}
            </p>
          </>
        )}
      </CardContent>
    </Card>
  )
}
