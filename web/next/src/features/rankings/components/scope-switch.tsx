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
import { useQuery } from '@tanstack/react-query';
import { useTranslation } from 'react-i18next';

import { Button } from '@/components/ui/button';
import { NativeSelect, NativeSelectOption } from '@/components/ui/native-select';

import { tenantOptionsQuery } from '../lib/api';

export type ScopeKind = 'tenant' | 'all';

/**
 * Platform-root only: choose between the caller's own tenant and the
 * cross-tenant board, and (cross-tenant) narrow it to one tenant. The tenant
 * list is a convenience: if it cannot be read the picker offers only
 * "All tenants" rather than blocking the board.
 */
export function ScopeSwitch(props: {
  kind: ScopeKind;
  tenantId: string;
  onKindChange: (kind: ScopeKind) => void;
  onTenantChange: (tenantId: string) => void;
}) {
  const { t } = useTranslation();
  const tenants = useQuery({
    ...tenantOptionsQuery,
    enabled: props.kind === 'all',
  });

  return (
    <div
      className='flex flex-wrap items-center gap-2'
      data-testid='rankings-scope'
    >
      <div role='group' aria-label={t('Scope')} className='flex gap-1'>
        <Button
          size='sm'
          variant={props.kind === 'tenant' ? 'default' : 'outline'}
          aria-pressed={props.kind === 'tenant'}
          data-testid='rankings-scope-tenant'
          onClick={() => props.onKindChange('tenant')}
        >
          {t('My tenant')}
        </Button>
        <Button
          size='sm'
          variant={props.kind === 'all' ? 'default' : 'outline'}
          aria-pressed={props.kind === 'all'}
          data-testid='rankings-scope-all'
          onClick={() => props.onKindChange('all')}
        >
          {t('All tenants')}
        </Button>
      </div>
      {props.kind === 'all' && (
        <NativeSelect
          size='sm'
          aria-label={t('Tenant')}
          data-testid='rankings-scope-tenant-filter'
          value={props.tenantId}
          onChange={(e) => props.onTenantChange(e.target.value)}
        >
          <NativeSelectOption value=''>{t('All tenants')}</NativeSelectOption>
          {(tenants.data ?? []).map((tn) => (
            <NativeSelectOption key={tn.id} value={tn.id}>
              {tn.name || tn.slug || tn.id}
            </NativeSelectOption>
          ))}
        </NativeSelect>
      )}
    </div>
  );
}
