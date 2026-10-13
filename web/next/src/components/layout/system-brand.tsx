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
import { Link } from '@tanstack/react-router'

import { BRAND_NAME, PRODUCT_NAME } from '@/lib/constants'

/** Logo and product name; links to the console home. */
export function SystemBrand() {
  return (
    <Link
      to='/dashboard'
      className='focus-visible:ring-ring flex min-w-0 items-center gap-2 rounded-md px-1 py-0.5 outline-hidden focus-visible:ring-2'
    >
      <img src='/next/logo.png' alt='' className='size-6 shrink-0 rounded-md' />
      <span className='flex min-w-0 items-baseline gap-1.5'>
        <span className='truncate text-sm font-semibold'>{BRAND_NAME}</span>
        <span className='text-muted-foreground hidden truncate text-xs sm:inline'>
          {PRODUCT_NAME}
        </span>
      </span>
    </Link>
  )
}
