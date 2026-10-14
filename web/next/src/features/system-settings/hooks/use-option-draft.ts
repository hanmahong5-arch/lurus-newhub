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
import { createContext, useContext } from 'react'

/**
 * Draft access for bespoke editors rendered inside an OptionForm (the model
 * price table edits the same JSON options the raw editor shows).
 */
export type OptionDraft = {
  /** Current value: the unsaved edit if there is one, else the saved value. */
  value: (key: string) => string
  /** The saved value as the form shows it. */
  saved: (key: string) => string
  setValue: (key: string, value: string) => void
  disabled: boolean
}

export const OptionDraftContext = createContext<OptionDraft | null>(null)

export function useOptionDraft(): OptionDraft {
  const draft = useContext(OptionDraftContext)
  if (!draft) throw new Error('useOptionDraft needs an OptionForm')
  return draft
}
