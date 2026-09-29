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
import React, { useEffect } from 'react';
import { useTranslation } from 'react-i18next';
import { useTableCompactMode } from '../../hooks/common/useTableCompactMode';

/**
 * HfDensityToggle — a small aria-pressed button that flips a table between
 * its normal row padding and the compact `hf-dense` variant (hifi-tokens.css,
 * L1 step 0). State lives in useTableCompactMode(tableKey), which already
 * persists to localStorage and stays in sync across tabs — this component
 * does not duplicate that storage, it only renders the control and forwards
 * the current value to the caller so the caller can add the `hf-dense`
 * className to its own <table>.
 *
 * Props:
 *   tableKey  required; passed straight through to useTableCompactMode, e.g.
 *             'v2-log' or 'v2-admin-users'. Two toggles with the same key
 *             share state (that is the hook's existing cross-tab contract).
 *   onChange  optional; called with the boolean compact value on mount and
 *             on every change, so a parent can mirror it onto its table
 *             className without reading localStorage itself.
 */
const HfDensityToggle = ({ tableKey, onChange }) => {
  const { t: tr } = useTranslation();
  const [compact, setCompactMode] = useTableCompactMode(tableKey);

  useEffect(() => {
    onChange?.(compact);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [compact]);

  return (
    <button
      type='button'
      className='btn ghost sm'
      aria-pressed={compact}
      data-testid={`density-toggle-${tableKey}`}
      onClick={() => setCompactMode(!compact)}
    >
      {tr('console.log.compact_rows', 'compact rows')}
    </button>
  );
};

export default HfDensityToggle;
