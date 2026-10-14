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
import { useQueryClient } from '@tanstack/react-query'
import {
  createContext,
  useCallback,
  useContext,
  useMemo,
  useState,
  type ReactNode,
} from 'react'

import { redemptionsQueryKey } from '../api'
import type { RedemptionDialog } from '../types'

interface RedemptionsContextValue {
  dialog: RedemptionDialog
  setDialog: (dialog: RedemptionDialog) => void
  /** Re-read the table after a write. */
  refresh: () => void
}

const RedemptionsContext = createContext<RedemptionsContextValue | null>(null)

export function RedemptionsProvider({ children }: { children: ReactNode }) {
  const qc = useQueryClient()
  const [dialog, setDialog] = useState<RedemptionDialog>(null)

  const refresh = useCallback(() => {
    void qc.invalidateQueries({ queryKey: redemptionsQueryKey })
  }, [qc])

  const value = useMemo(
    () => ({ dialog, setDialog, refresh }),
    [dialog, refresh]
  )
  return <RedemptionsContext value={value}>{children}</RedemptionsContext>
}

// eslint-disable-next-line react-refresh/only-export-components
export function useRedemptions(): RedemptionsContextValue {
  const ctx = useContext(RedemptionsContext)
  if (!ctx) {
    throw new Error('useRedemptions has to be used within <RedemptionsProvider>')
  }
  return ctx
}
