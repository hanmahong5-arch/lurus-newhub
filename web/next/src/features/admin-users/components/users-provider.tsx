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

import { usersQueryKey } from '../api'
import type { AdminUser, UsersDialog } from '../types'

interface UsersContextValue {
  dialog: UsersDialog
  setDialog: (dialog: UsersDialog) => void
  /** The Adjust Quota dialog stacks on top of the edit drawer. */
  quotaUser: AdminUser | null
  setQuotaUser: (user: AdminUser | null) => void
  /** Re-read the table after a write. */
  refresh: () => void
}

const UsersContext = createContext<UsersContextValue | null>(null)

export function UsersProvider({ children }: { children: ReactNode }) {
  const qc = useQueryClient()
  const [dialog, setDialog] = useState<UsersDialog>(null)
  const [quotaUser, setQuotaUser] = useState<AdminUser | null>(null)

  const refresh = useCallback(() => {
    void qc.invalidateQueries({ queryKey: usersQueryKey })
  }, [qc])

  const value = useMemo(
    () => ({ dialog, setDialog, quotaUser, setQuotaUser, refresh }),
    [dialog, quotaUser, refresh]
  )
  return <UsersContext value={value}>{children}</UsersContext>
}

// eslint-disable-next-line react-refresh/only-export-components
export function useUsers(): UsersContextValue {
  const ctx = useContext(UsersContext)
  if (!ctx) throw new Error('useUsers has to be used within <UsersProvider>')
  return ctx
}
