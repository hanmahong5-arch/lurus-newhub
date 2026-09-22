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
import HfVendorIcon from './HfVendorIcon';
import { CHANNEL_OPTIONS } from '../../constants/channel.constants';

const row = { display: 'inline-flex', alignItems: 'center', gap: 6 };

/** A model name with its vendor logo; `fallback` when there is no name. */
export const HfModelName = ({ model, vendor, size = 14, fallback = '—' }) =>
  model ? (
    <span style={row}>
      <HfVendorIcon size={size} model={model} vendor={vendor} />
      {model}
    </span>
  ) : (
    fallback
  );

export const CHANNEL_TYPE_LABEL = Object.fromEntries(
  CHANNEL_OPTIONS.map((o) => [o.value, o.label]),
);

/** A channel's provider type by name and logo, not its bare integer id. */
export const ChannelTypeLabel = ({ type }) => {
  if (type == null) return '—';
  const label = CHANNEL_TYPE_LABEL[Number(type)];
  return (
    <span data-testid='channel-type-label' style={row}>
      <HfVendorIcon vendor={label} size={14} />
      <span>{label || `#${type}`}</span>
    </span>
  );
};

/**
 * The provider-type picker. It used to be a bare number input: an operator
 * had to know that 36 means Suno. The value is still the numeric id; an id
 * this build does not list stays selectable as "#id" rather than vanishing.
 */
export const ChannelTypeSelect = ({ value, onChange, style }) => (
  <select
    style={style}
    value={value}
    onChange={onChange}
    data-testid='channel-type-select'
  >
    {!CHANNEL_TYPE_LABEL[Number(value)] && (
      <option value={value}>{`#${value}`}</option>
    )}
    {CHANNEL_OPTIONS.map((o) => (
      <option key={o.value} value={o.value}>
        {`${o.label} · ${o.value}`}
      </option>
    ))}
  </select>
);

export default HfModelName;
