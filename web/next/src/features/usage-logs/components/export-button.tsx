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
import { useMutation } from '@tanstack/react-query'
import { Download, Loader2 } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { Button } from '@/components/ui/button'
import { toApiError } from '@/lib/api'

import { exportLogsCsv } from '../api'
import { downloadTextFile, fileStamp } from '../lib/download'
import type { LogFilters } from '../lib/logs'

/**
 * CSV export of the rows the applied filters select. The export route has
 * its own row cap and no username / request-id filter, so a search that
 * relies on one of those would export more than the table shows: the button
 * is disabled for those instead of silently widening the file.
 */
export function ExportButton(props: { filters: LogFilters }) {
  const { t } = useTranslation()
  const f = props.filters
  const unsupported =
    f.requestId.trim() !== '' ||
    f.sessionId.trim() !== '' ||
    (f.tenantWide && f.username.trim() !== '')

  const exporter = useMutation({
    mutationFn: () => exportLogsCsv(f),
    onSuccess: (csv) => {
      downloadTextFile(`usage-logs-${fileStamp()}.csv`, csv)
      toast.success(t('Export ready'))
    },
    onError: (e) => toast.error(toApiError(e).message),
  })

  return (
    <Button
      type='button'
      variant='outline'
      disabled={exporter.isPending || unsupported}
      title={
        unsupported
          ? t('The export has no request, session or user filter. Clear them to export.')
          : t('Export follows the date, model, token and type filters.')
      }
      onClick={() => exporter.mutate()}
    >
      {exporter.isPending ? <Loader2 className='animate-spin' /> : <Download />}
      {exporter.isPending ? t('Exporting...') : t('Export CSV')}
    </Button>
  )
}
