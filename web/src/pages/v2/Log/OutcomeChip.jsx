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

// The outcome tag of one trace row. `outcome` is outcomeTag(row) from the
// Log page. Only error rows are clickable — the API has no "success only" filter.
const OutcomeChip = ({ outcome: o, errorsOnly, onToggle, tr }) => {
  const label = tr(`console.log.outcome_${o.label}`, o.label);
  if (o.label !== 'error') return <span className={o.cls}>{label}</span>;
  const hint = tr(
    'console.log.filter_errors_hint',
    'show only failed requests',
  );
  const click = (e) => {
    e.stopPropagation();
    onToggle();
  };
  return (
    <button
      type='button'
      className={o.cls}
      aria-pressed={errorsOnly}
      title={hint}
      onClick={click}
      style={{ background: 'transparent', cursor: 'pointer' }}
    >
      {label}
    </button>
  );
};

export default OutcomeChip;
