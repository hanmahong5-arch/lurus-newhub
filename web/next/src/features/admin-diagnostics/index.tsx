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
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { RefreshCw } from 'lucide-react'
import { useTranslation } from 'react-i18next'

import { ErrorState } from '@/components/error-state'
import { LoadingState } from '@/components/loading-state'
import { SectionPageLayout } from '@/components/section-page-layout'
import { Button } from '@/components/ui/button'

import { diagnosticsKeys, fetchAffinity, fetchTotpStats } from './api'
import { AffinityPanel } from './components/affinity-panel'
import { TotpPanel } from './components/totp-panel'
import { classifyReadError, type ReadFailure } from './lib/load-status'

export function AdminDiagnosticsPage() {
  const { t } = useTranslation()
  const qc = useQueryClient()

  // errorToast:false: each panel renders its own unavailable marker, so the
  // global handler's toast would only repeat it without saying which panel.
  const affinity = useQuery({
    queryKey: diagnosticsKeys.affinity,
    queryFn: fetchAffinity,
    retry: false,
    meta: { errorToast: false },
  })
  const totp = useQuery({
    queryKey: diagnosticsKeys.totp,
    queryFn: fetchTotpStats,
    retry: false,
    meta: { errorToast: false },
  })

  // Each read is classified on its own, so one endpoint's genuine failure does
  // not blank the other panel. A refusal, though, is about the person and not
  // the endpoint: either one makes the whole page "root required". 'forbidden'
  // and 'unauthenticated' are not told apart here.
  const failureOf = (q: {
    isError: boolean
    error: unknown
  }): ReadFailure | null => (q.isError ? classifyReadError(q.error) : null)
  const affinityFailure = failureOf(affinity)
  const totpFailure = failureOf(totp)
  const refused = [affinityFailure, totpFailure].some(
    (f) => f === 'forbidden' || f === 'unauthenticated'
  )
  const loading = affinity.isPending || totp.isPending

  const refresh = () => qc.invalidateQueries({ queryKey: diagnosticsKeys.all })

  let content: React.ReactNode
  if (refused) {
    content = (
      <ErrorState
        title={t('Root access required')}
        description={t(
          'You do not have permission to view diagnostics. Contact a platform administrator.'
        )}
      />
    )
  } else if (loading) {
    content = <LoadingState />
  } else {
    content = (
      <div className='grid gap-4'>
        <AffinityPanel
          snapshot={affinity.data}
          failed={affinityFailure !== null}
          onRetry={() => void affinity.refetch()}
        />
        <TotpPanel
          stats={totp.data}
          failed={totpFailure !== null}
          onRetry={() => void totp.refetch()}
        />
      </div>
    )
  }

  return (
    <SectionPageLayout>
      <SectionPageLayout.Title>{t('Diagnostics')}</SectionPageLayout.Title>
      <SectionPageLayout.Actions>
        <Button
          size='sm'
          variant='outline'
          onClick={() => void refresh()}
          disabled={affinity.isFetching || totp.isFetching}
        >
          <RefreshCw className='size-4' />
          {t('Refresh')}
        </Button>
      </SectionPageLayout.Actions>
      <SectionPageLayout.Content>{content}</SectionPageLayout.Content>
    </SectionPageLayout>
  )
}
