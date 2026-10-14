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
import { Outlet } from '@tanstack/react-router'

import { SkipToMain } from '@/components/skip-to-main'

import { PublicFooter } from './components/public-footer'
import { PublicHeader } from './components/public-header'

/**
 * Shell of every page anyone may open without a session: top bar, the routed
 * page, footer. It owns the page's single main landmark. No request here may
 * trigger the sign-in redirect; see api.ts and queries.ts.
 */
export function PublicLayout() {
  return (
    <div className='bg-background text-foreground relative flex min-h-svh flex-col overflow-x-clip'>
      <SkipToMain />
      <PublicHeader />
      <main id='content' className='flex min-w-0 flex-1 flex-col'>
        <Outlet />
      </main>
      <PublicFooter />
    </div>
  )
}
