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

// The context-tiers toggle button, split out of Pricing/index.jsx. A model
// with zero tiers is not "disabled" — it is the entry point for adding the
// first one — so it stays a real button, just visually quieter than a model
// that already has tiers configured.

import React from 'react';

/**
 * @param {object} p
 * @param {number} p.count       row.context_tiers.length
 * @param {() => void} p.onClick opens/closes the tier editor row
 * @param {Function} p.tr
 * @param {string} [p.testId]
 */
const TierCell = ({ count, onClick, tr, testId }) => {
  const tiered = count > 0;
  return (
    <button
      type='button'
      className='btn'
      style={{
        fontSize: 10,
        padding: '2px 8px',
        ...(tiered
          ? { fontWeight: 600 }
          : { color: 'var(--hf-ink-3)', fontWeight: 400 }),
      }}
      data-testid={testId}
      onClick={onClick}
    >
      {tiered
        ? tr('console.pricing.context_tiers_count', { count })
        : `${tr('console.pricing.tiers_none', 'not tiered')} +`}
    </button>
  );
};

export default TierCell;
