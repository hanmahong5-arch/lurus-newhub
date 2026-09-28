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

import React, { useEffect, useId, useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';
import HfDialog, { HfDialogFooter } from '../hifi/HfDialog';

// Tier 1.3 (2026-05-19) — ConfirmDialog replaces window.confirm() at the
// three destructive surfaces in the v2 console (token revoke / rotate,
// channel delete, tenant enable/disable/suspend).
//
// The plain confirm() popup made it too easy to nuke prod tokens by
// reflex. Tier 1 customer-experience plan classified this as pain
// point #2 ("我不敢按这个按钮").
//
// Required-typed-confirmation pattern (e.g. type the resource name to
// arm the Confirm button) gives the user a forcing function without
// being patronising — they CAN go through, but only after a moment of
// attention. The pattern is borrowed from GitHub's repo-delete dialog
// and Stripe's account-close flow, both validated on similar audiences.
//
// Behaviour contract:
//
//   - Confirm button is disabled until the input value === confirmText
//     (strict, case-sensitive equality — no trim, no fuzzy match).
//   - Esc cancels; Enter (while input focused + matched) confirms.
//   - onConfirm may return a Promise — during pending the dialog is busy:
//     Confirm, Cancel, the X and the input are disabled and Escape /
//     backdrop are refused, so a double-click can't trigger a second
//     mutation and a reflex Escape can't throw the in-flight one away.
//   - Focus lands in the input on open; HfDialog owns the tab-trap,
//     focus return and scroll lock.
//
// Cycle 18: rebuilt on components/hifi/HfDialog. This was the last v2
// surface still reaching Semi's Modal/Input/Button, and the only way to
// unit-test it was to shim all three — which is how Escape, backdrop and
// the accessible name went unverified for a year. The props are unchanged;
// only the surface under them moved.

// Semi Button `type` → hifi .btn modifier (.btn.danger / .btn.warning take a
// solid fill in hifi-tokens.css, like .primary).
const BUTTON_CLASS = {
  danger: 'btn danger',
  warning: 'btn warning',
  primary: 'btn primary',
};

const ConfirmDialog = ({
  visible,
  title,
  consequenceList = [],
  confirmText,
  confirmButtonText,
  confirmButtonType = 'danger',
  onConfirm,
  onCancel,
}) => {
  const { t } = useTranslation();
  const inputId = useId();
  const [inputValue, setInputValue] = useState('');
  const [pending, setPending] = useState(false);
  const inputRef = useRef(null);

  // Reset the typed value every time the dialog opens. Without this a
  // user who hit Cancel after typing "alpha" would find the field still
  // primed when reopening the dialog for a DIFFERENT resource. State lives
  // here rather than inside the (unmounted-while-hidden) panel so a
  // resolved onConfirm can still flip `pending` back after the caller
  // closes the dialog.
  useEffect(() => {
    if (visible) {
      setInputValue('');
      setPending(false);
    }
  }, [visible]);

  const armed = inputValue === confirmText && !pending;

  const handleConfirm = async () => {
    if (!armed) return;
    setPending(true);
    try {
      const ret = onConfirm();
      if (ret && typeof ret.then === 'function') {
        await ret;
      }
    } finally {
      // The caller is responsible for closing the dialog on success.
      // We only flip pending back so a failed onConfirm leaves the
      // button armed for retry rather than stuck in loading.
      setPending(false);
    }
  };

  const handleKeyDown = (e) => {
    if (e.key === 'Enter' && armed) {
      e.preventDefault();
      handleConfirm();
    }
  };

  if (!visible) return null;

  return (
    <HfDialog
      title={title}
      onClose={onCancel}
      busy={pending}
      width={460}
      initialFocusRef={inputRef}
      testId='confirm-dialog'
      backdropTestId='confirm-dialog-backdrop'
    >
      {consequenceList.length > 0 && (
        <ul
          data-testid='confirm-dialog-consequences'
          style={{
            margin: 0,
            padding: '10px 12px 10px 28px',
            borderLeft: '3px solid var(--hf-err)',
            background: 'var(--hf-sunken)',
            color: 'var(--hf-err)',
            fontSize: 13,
            lineHeight: 1.6,
            listStyle: 'disc',
          }}
        >
          {consequenceList.map((line, i) => (
            <li key={i}>{line}</li>
          ))}
        </ul>
      )}
      <div>
        <label
          htmlFor={inputId}
          style={{ display: 'block', marginBottom: 6, fontSize: 13 }}
        >
          {t('请输入 {{name}} 以确认操作:', { name: confirmText })}
        </label>
        <input
          id={inputId}
          ref={inputRef}
          type='text'
          className='hf-input'
          style={{ width: '100%', boxSizing: 'border-box' }}
          value={inputValue}
          onChange={(e) => setInputValue(e.target.value)}
          onKeyDown={handleKeyDown}
          disabled={pending}
          autoComplete='off'
          spellCheck={false}
          data-testid='confirm-dialog-input'
          placeholder={confirmText}
        />
      </div>
      <HfDialogFooter>
        <button
          type='button'
          className='btn ghost'
          onClick={onCancel}
          disabled={pending}
        >
          {t('取消')}
        </button>
        <button
          type='button'
          className={BUTTON_CLASS[confirmButtonType] || 'btn'}
          disabled={!armed}
          aria-busy={pending || undefined}
          onClick={handleConfirm}
          data-testid='confirm-dialog-confirm'
        >
          {confirmButtonText || t('删除')}
        </button>
      </HfDialogFooter>
    </HfDialog>
  );
};

export default ConfirmDialog;
