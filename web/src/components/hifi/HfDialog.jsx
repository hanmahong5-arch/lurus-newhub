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

// Usage — mount conditionally, exactly like the hand-rolled overlays it replaces:
//   {open && (
//     <HfDialog title={tr('…')} onClose={close} as='form' onSubmit={submit}
//       busy={saving} width={420} initialFocusRef={nameRef} testId='x-dialog'>
//       …fields…
//       <HfDialogFooter>
//         <button type='button' className='btn ghost' onClick={close}>cancel</button>
//         <button type='submit' className='btn primary' disabled={saving}>save</button>
//       </HfDialogFooter>
//     </HfDialog>
//   )}
// variant='side' (+ wide) gives the marketplace side sheet instead of the
// centred panel. Escape / backdrop / the X are all refused while `busy`.

import React, { useEffect, useId, useRef } from 'react';
import { useTranslation } from 'react-i18next';

/**
 * HfDialog — the one modal surface for /console/v2/*.
 *
 * Deliberately not a Semi `Modal` (see HfToast.jsx for why hifi stays off
 * Semi) and deliberately not a portal: the panel must stay a descendant of
 * the `.hf` scope or every token in hifi-tokens.css stops applying to it.
 *
 * What it guarantees, so no page has to:
 *   - role="dialog" + aria-modal on the panel, named by the title
 *     (aria-labelledby) or `ariaLabel`;
 *   - focus moves in on open (initialFocusRef → first focusable in the body →
 *     the panel) and back to the opener on close, unless the opener has
 *     since left the document (navigation from inside the drawer);
 *   - Tab / Shift+Tab wrap inside the panel;
 *   - Escape closes only the dialog on top of the stack, only when idle, and
 *     only when the key was pressed inside this panel or on nothing at all —
 *     Semi's step-up modal portals to <body>, and a keypress in it must not
 *     also close the dialog underneath;
 *   - a backdrop click closes only if the mousedown landed on the backdrop
 *     too, so releasing a drag-select over it does not throw the form away;
 *   - body scroll is locked while any dialog is open (ref-counted).
 *
 * z-order stays below 1000 so HfToast and the Semi step-up modal render
 * above it (hifi-tokens.css, toast primitive).
 *
 * Props:
 *   title             node shown in the header; also the accessible name.
 *   ariaLabel         accessible name when there is no title.
 *   onClose           () => void. Escape, backdrop, X.
 *   variant           'center' (default) | 'side' (reuses .hf-drawer).
 *   width             centred panel width, px (default 420).
 *   wide              side variant: .hf-drawer-wide.
 *   as                'div' (default) | 'form' — the panel element itself.
 *   onSubmit          forwarded to the panel when as='form'.
 *   busy              true while a request is in flight: no dismissal at all.
 *   dismissOnBackdrop default true. False for dialogs holding a one-time
 *                     secret the operator has not copied yet.
 *   closeButton       default true. The X in the header.
 *   initialFocusRef   ref of the element to focus on open.
 *   testId / backdropTestId  data-testid on the panel / the backdrop.
 */

// Top of this stack is the only dialog Escape may close. Module scope, not
// context: the two Models drawers and the Users 2FA pair are siblings in
// different subtrees and still need a single order.
const stack = [];
let scrollLocks = 0;
let bodyOverflowBefore = '';

const FOCUSABLE =
  'a[href], button:not([disabled]), input:not([disabled]):not([type="hidden"]), ' +
  'select:not([disabled]), textarea:not([disabled]), [tabindex]:not([tabindex="-1"])';

const focusables = (root) =>
  root ? Array.from(root.querySelectorAll(FOCUSABLE)) : [];

// A keydown with no element under it (dispatched on window, the document or
// a bare body) belongs to nobody in particular; one from an element belongs
// to that element's dialog only. Window is not a Node, hence the shape test
// rather than an identity check against the (possibly proxied) global.
const isOurs = (panel, target) =>
  !(target instanceof Node) ||
  target === document ||
  target === document.body ||
  (panel && panel.contains(target));

const HfDialog = ({
  title,
  ariaLabel,
  onClose,
  variant = 'center',
  width = 420,
  wide = false,
  as = 'div',
  onSubmit,
  busy = false,
  dismissOnBackdrop = true,
  closeButton = true,
  initialFocusRef,
  testId,
  backdropTestId,
  children,
}) => {
  const { t } = useTranslation();
  const titleId = useId();
  const panelRef = useRef(null);
  const bodyRef = useRef(null);
  const downTargetRef = useRef(null);
  // Refs so the window listener is registered once per mount and still sees
  // the latest props.
  const closeRef = useRef(onClose);
  const busyRef = useRef(busy);
  closeRef.current = onClose;
  busyRef.current = busy;

  // Stack membership + Escape, one registration per mount.
  useEffect(() => {
    const token = {};
    stack.push(token);
    const onKey = (ev) => {
      if (ev.key !== 'Escape' || busyRef.current) return;
      if (stack[stack.length - 1] !== token) return;
      if (!isOurs(panelRef.current, ev.target)) return;
      closeRef.current?.();
    };
    window.addEventListener('keydown', onKey);
    return () => {
      window.removeEventListener('keydown', onKey);
      const i = stack.indexOf(token);
      if (i >= 0) stack.splice(i, 1);
    };
  }, []);

  // Scroll lock, ref-counted across stacked dialogs.
  useEffect(() => {
    if (scrollLocks++ === 0) {
      bodyOverflowBefore = document.body.style.overflow;
      document.body.style.overflow = 'hidden';
    }
    return () => {
      if (--scrollLocks === 0)
        document.body.style.overflow = bodyOverflowBefore;
    };
  }, []);

  // Focus in on open, back to the opener on close.
  useEffect(() => {
    const opener = document.activeElement;
    const panel = panelRef.current;
    // A child with autoFocus already took focus during commit; leave it.
    if (panel && !panel.contains(document.activeElement)) {
      const target =
        initialFocusRef?.current ||
        focusables(bodyRef.current)[0] ||
        focusables(panel)[0] ||
        panel;
      target.focus();
    }
    return () => {
      if (opener && opener.isConnected && typeof opener.focus === 'function') {
        opener.focus();
      }
    };
    // initialFocusRef is read once, on open.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  const trapTab = (ev) => {
    if (ev.key !== 'Tab') return;
    const list = focusables(panelRef.current);
    if (list.length === 0) {
      ev.preventDefault();
      return;
    }
    const first = list[0];
    const last = list[list.length - 1];
    const active = document.activeElement;
    if (ev.shiftKey && (active === first || !list.includes(active))) {
      ev.preventDefault();
      last.focus();
    } else if (!ev.shiftKey && (active === last || !list.includes(active))) {
      ev.preventDefault();
      first.focus();
    }
  };

  const onBackdropMouseDown = (ev) => {
    downTargetRef.current = ev.target;
  };
  const onBackdropClick = (ev) => {
    const startedHere = downTargetRef.current === ev.currentTarget;
    downTargetRef.current = null;
    if (ev.target !== ev.currentTarget || !startedHere) return;
    if (!dismissOnBackdrop || busy) return;
    onClose?.();
  };

  const side = variant === 'side';
  const Panel = side ? 'aside' : as;
  const panelClass = side
    ? `hf-drawer${wide ? ' hf-drawer-wide' : ''}`
    : 'hf-dialog';

  return (
    <div
      className={side ? 'hf-drawer-backdrop' : 'hf-dialog-backdrop'}
      data-testid={backdropTestId}
      onMouseDown={onBackdropMouseDown}
      onClick={onBackdropClick}
    >
      <Panel
        ref={panelRef}
        className={panelClass}
        role='dialog'
        aria-modal='true'
        aria-labelledby={title != null ? titleId : undefined}
        aria-label={title == null ? ariaLabel : undefined}
        aria-busy={busy || undefined}
        tabIndex={-1}
        data-testid={testId}
        onKeyDown={trapTab}
        onSubmit={as === 'form' && !side ? onSubmit : undefined}
        style={side ? undefined : { width }}
      >
        {(title != null || closeButton) && (
          <div className='hf-dialog-head'>
            {title != null && (
              <div id={titleId} className='strong' style={{ fontSize: 15 }}>
                {title}
              </div>
            )}
            {closeButton && (
              <button
                type='button'
                className='btn ghost sm'
                onClick={onClose}
                disabled={busy}
                aria-label={t('console.common.close', 'close')}
              >
                ✕
              </button>
            )}
          </div>
        )}
        {/* The side sheet lays its own content out; only the centred panel
            wants the flex-column body. */}
        <div ref={bodyRef} className={side ? undefined : 'hf-dialog-body'}>
          {children}
        </div>
      </Panel>
    </div>
  );
};

/** The ghost-cancel / primary-submit row every form dialog ends with. */
export const HfDialogFooter = ({ children, style }) => (
  <div className='hf-dialog-footer' style={style}>
    {children}
  </div>
);

export default HfDialog;
