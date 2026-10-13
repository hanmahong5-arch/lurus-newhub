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

import { usePublicPricing } from '../queries'

/**
 * Live counts from the public price list. These are the only numbers on the
 * landing page and they are read, not written: when the list cannot be
 * loaded or is empty the strip does not render at all.
 */
export function CatalogueStrip() {
  const { t } = useTranslation()
  const { data } = usePublicPricing()
  // The endpoint lists priced models only; a row that says otherwise is not
  // counted as one.
  const rows = data?.rows.filter((row) => row.price_configured !== false) ?? []
  if (!data || rows.length === 0) return null

  const vendors = new Set<string>()
  const endpoints = new Set<string>()
  const byId = new Map(data.vendors.map((v) => [v.id, v.name]))
  for (const row of rows) {
    const vendor = (row.vendor_id && byId.get(row.vendor_id)) || row.owner_by
    if (vendor) vendors.add(vendor)
    for (const endpoint of row.supported_endpoint_types ?? []) {
      endpoints.add(endpoint)
    }
  }

  const stats = [
    { value: rows.length, label: t('priced models') },
    ...(vendors.size > 0 ? [{ value: vendors.size, label: t('vendors') }] : []),
    ...(endpoints.size > 0
      ? [{ value: endpoints.size, label: t('API formats') }]
      : []),
  ]

  return (
    <section
      aria-label={t('Live catalogue')}
      className='border-border/40 relative z-10 border-y px-6 py-10'
    >
      <dl className='mx-auto grid max-w-3xl grid-cols-3 gap-6 text-center'>
        {stats.map((stat) => (
          <div key={stat.label} className='flex flex-col-reverse'>
            <dt className='text-muted-foreground mt-1 text-xs md:text-sm'>
              {stat.label}
            </dt>
            <dd className='text-3xl font-bold tracking-tight tabular-nums md:text-4xl'>
              {stat.value.toLocaleString()}
            </dd>
          </div>
        ))}
      </dl>
    </section>
  )
}
