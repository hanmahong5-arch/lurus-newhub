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
import React, { useEffect, useRef, useState } from 'react';

// ─── Inline setting editor ────────────────────────────────────────────────────

const InlineEdit = ({ value, onSave, onCancel }) => {
  const [v, setV] = useState(value);
  const ref = useRef(null);

  useEffect(() => {
    ref.current?.select();
  }, []);

  const commit = () => {
    if (v.trim() !== value) onSave(v.trim());
    else onCancel();
  };

  return (
    <input
      ref={ref}
      style={{
        fontFamily: 'var(--hf-mono)',
        fontSize: 12,
        padding: '3px 6px',
        width: '100%',
        border: '1px solid var(--hf-rule)',
        background: 'var(--hf-sunken)',
        color: 'var(--hf-ink)',
        borderRadius: 2,
        outline: 'none',
      }}
      value={v}
      onChange={(e) => setV(e.target.value)}
      onKeyDown={(e) => {
        if (e.key === 'Enter') commit();
        if (e.key === 'Escape') onCancel();
      }}
      onBlur={commit}
    />
  );
};

export default InlineEdit;
