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
import { createContext, useContext, useState } from 'react'

import { readThemePreference, writeThemePreference } from '@/lib/theme-storage'

export type Collapsible = 'offcanvas' | 'icon' | 'none'
export type Variant = 'inset' | 'sidebar' | 'floating'

// localStorage keys (cookies are shared across ports, so a local instance
// would otherwise restore another instance's layout).
const LAYOUT_COLLAPSIBLE_KEY = 'newhub-console:layout:v1:collapsible'
const LAYOUT_VARIANT_KEY = 'newhub-console:layout:v1:variant'
const COLLAPSIBLE_VALUES: ReadonlySet<Collapsible> = new Set([
  'offcanvas',
  'icon',
  'none',
])
const VARIANT_VALUES: ReadonlySet<Variant> = new Set([
  'inset',
  'sidebar',
  'floating',
])

// Default values
const DEFAULT_VARIANT: Variant = 'inset'
const DEFAULT_COLLAPSIBLE: Collapsible = 'icon'

type LayoutContextType = {
  resetLayout: () => void

  defaultCollapsible: Collapsible
  collapsible: Collapsible
  setCollapsible: (collapsible: Collapsible) => void

  defaultVariant: Variant
  variant: Variant
  setVariant: (variant: Variant) => void
}

const LayoutContext = createContext<LayoutContextType | null>(null)

type LayoutProviderProps = {
  children: React.ReactNode
}

export function LayoutProvider({ children }: LayoutProviderProps) {
  const [collapsible, _setCollapsible] = useState<Collapsible>(() =>
    readThemePreference(
      LAYOUT_COLLAPSIBLE_KEY,
      COLLAPSIBLE_VALUES,
      DEFAULT_COLLAPSIBLE
    )
  )

  const [variant, _setVariant] = useState<Variant>(() =>
    readThemePreference(LAYOUT_VARIANT_KEY, VARIANT_VALUES, DEFAULT_VARIANT)
  )

  const setCollapsible = (newCollapsible: Collapsible) => {
    _setCollapsible(newCollapsible)
    writeThemePreference(
      LAYOUT_COLLAPSIBLE_KEY,
      newCollapsible === DEFAULT_COLLAPSIBLE ? null : newCollapsible
    )
  }

  const setVariant = (newVariant: Variant) => {
    _setVariant(newVariant)
    writeThemePreference(
      LAYOUT_VARIANT_KEY,
      newVariant === DEFAULT_VARIANT ? null : newVariant
    )
  }

  const resetLayout = () => {
    setCollapsible(DEFAULT_COLLAPSIBLE)
    setVariant(DEFAULT_VARIANT)
  }

  const contextValue: LayoutContextType = {
    resetLayout,
    defaultCollapsible: DEFAULT_COLLAPSIBLE,
    collapsible,
    setCollapsible,
    defaultVariant: DEFAULT_VARIANT,
    variant,
    setVariant,
  }

  return <LayoutContext value={contextValue}>{children}</LayoutContext>
}

// Define the hook for the provider
// eslint-disable-next-line react-refresh/only-export-components
export function useLayout() {
  const context = useContext(LayoutContext)
  if (!context) {
    throw new Error('useLayout must be used within a LayoutProvider')
  }
  return context
}
