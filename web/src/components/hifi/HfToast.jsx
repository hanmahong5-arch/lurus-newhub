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

// `.hf` scope toast primitive — replaces the Semi UI `Toast` popup for
// /console/v2/* so feedback doesn't visually leak the ant-design aesthetic
// into the hi-fi editorial console (see helpers/utils.jsx showError/
// showSuccess/showWarning/showInfo, which route here whenever the caller
// is on a /console/v2/* route).
//
// State lives in module scope (not a React context) so `hfToast.error(...)`
// can be called from plain functions (helpers/utils.jsx) without a hook,
// mirroring the imperative Semi `Toast.error()` API it replaces. The host
// component below is a thin subscriber; mounting/unmounting it (e.g. across
// route navigation) does not drop queued toasts because the array itself
// is held here, not in the host's local state.

import React, { useEffect, useState } from 'react';

let toastSeq = 0;
let toasts = [];
const listeners = new Set();

function notify() {
  listeners.forEach((fn) => fn(toasts));
}

function dismiss(id) {
  toasts = toasts.filter((item) => item.id !== id);
  notify();
}

function push(type, message, autoCloseMs) {
  if (!message) return;
  const id = ++toastSeq;
  toasts = [...toasts, { id, type, message }];
  notify();
  if (autoCloseMs !== false) {
    window.setTimeout(() => dismiss(id), autoCloseMs || 3200);
  }
  return id;
}

export const hfToast = {
  error: (message) => push('err', message, 5000),
  success: (message) => push('ok', message, 3200),
  warning: (message) => push('warn', message, 4000),
  info: (message) => push('info', message, 3200),
  dismiss,
};

export function HfToastHost() {
  const [items, setItems] = useState(toasts);

  useEffect(() => {
    listeners.add(setItems);
    setItems(toasts);
    return () => listeners.delete(setItems);
  }, []);

  if (items.length === 0) return null;

  return (
    <div className='hf-toast-host' aria-live='polite' aria-atomic='false'>
      {items.map((item) => (
        <div
          key={item.id}
          className={`hf-toast ${item.type}`}
          role={item.type === 'err' ? 'alert' : 'status'}
        >
          <span className={`dot ${item.type}`} style={{ marginTop: 5 }} />
          <span className='msg'>{item.message}</span>
          <button
            type='button'
            className='close'
            aria-label='dismiss'
            onClick={() => dismiss(item.id)}
          >
            ×
          </button>
        </div>
      ))}
    </div>
  );
}
