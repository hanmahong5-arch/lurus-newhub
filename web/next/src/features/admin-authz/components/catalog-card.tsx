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
import { ShieldCheck } from 'lucide-react'
import { useTranslation } from 'react-i18next'

import { StatusBadge } from '@/components/status-badge'
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from '@/components/ui/card'

import type { AuthzCatalog } from '../types'

/** Read-only view of what a grant may name, as the server defines it. */
export function CatalogCard(props: { catalog: AuthzCatalog }) {
  const { t } = useTranslation()
  const { resources, roles } = props.catalog

  return (
    <Card data-testid='authz-catalog'>
      <CardHeader>
        <CardTitle className='flex items-center gap-2'>
          <ShieldCheck className='size-4' />
          {t('Permission catalog')}
        </CardTitle>
        <CardDescription>
          {t('The resources and actions a grant can name.')}
        </CardDescription>
      </CardHeader>
      <CardContent className='flex flex-col gap-3'>
        {resources.length === 0 ? (
          <p className='text-muted-foreground text-sm'>
            {t('The server lists no grantable permissions.')}
          </p>
        ) : (
          resources.map((r) => (
            <div
              key={r.resource}
              className='flex flex-wrap items-center gap-2'
              data-testid={`authz-catalog-${r.resource}`}
            >
              <span className='font-mono text-sm font-medium'>
                {r.resource}
              </span>
              <span className='text-muted-foreground text-xs'>/</span>
              {r.actions.map((a) => (
                <StatusBadge
                  key={a}
                  label={a}
                  variant='info'
                  copyable={false}
                  className='font-mono'
                />
              ))}
            </div>
          ))
        )}
        {roles.length > 0 && (
          <div className='text-muted-foreground border-t pt-3 text-xs'>
            {t('Fixed roles')}:{' '}
            {roles
              .map((r) => `${r.name} (${t('min role')} ${r.min_role})`)
              .join(' · ')}
          </div>
        )}
      </CardContent>
    </Card>
  )
}
