/*
Copyright (C) 2025 QuantumNous

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

import React from 'react';
import { useTranslation } from 'react-i18next';
import { LuDownload } from 'react-icons/lu';

/**
 * ExportCsvButton — the trace tab's "export CSV" affordance, pulled out of
 * Log/index.jsx (cycle-19 L4) only to keep that file under its line-count
 * ceiling; the URL it builds is unchanged from before the extraction.
 *
 * GET /api/v2/:tenant_slug/logs/export takes the same model/token/time
 * filters the trace table itself uses — export always follows what is on
 * screen, not a separate export-only filter set.
 */
const ExportCsvButton = ({
  tenantSlug,
  filterModel,
  filterToken,
  filterStart,
  filterEnd,
}) => {
  const { t: tr } = useTranslation();
  const onClick = () => {
    const params = new URLSearchParams();
    if (filterModel) params.set('model_name', filterModel);
    if (filterToken) params.set('token_name', filterToken);
    if (filterStart)
      params.set(
        'start_time',
        String(Math.floor(new Date(filterStart).getTime() / 1000)),
      );
    if (filterEnd)
      params.set(
        'end_time',
        String(Math.floor(new Date(filterEnd).getTime() / 1000)),
      );
    const qs = params.toString();
    window.location.href =
      `/api/v2/${tenantSlug}/logs/export` + (qs ? `?${qs}` : '');
  };

  return (
    <button
      type='button'
      className='btn ghost'
      data-testid='log-export-btn'
      onClick={onClick}
    >
      <LuDownload size={12} aria-hidden='true' />{' '}
      {tr('console.log.export_csv', 'export CSV')}
    </button>
  );
};

export default ExportCsvButton;
