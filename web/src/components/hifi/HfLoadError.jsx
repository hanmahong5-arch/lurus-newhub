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
import { useTranslation } from 'react-i18next';

/**
 * HfLoadError — the one way a v2 console page says "this read failed".
 *
 * Every panel that can no longer pretend a failed fetch was an empty result
 * (cycle-13 L7 / cycle-14 L8) renders this instead of its body, so the
 * failure looks and behaves the same everywhere:
 *
 *   - role="alert": screen readers announce it the moment it replaces the
 *     content, instead of the reader finding a silent gap;
 *   - the retry button goes busy (disabled + "Retrying…") while the caller's
 *     onRetry promise is pending, so a double click cannot fire two fetches
 *     and the operator can see the click registered;
 *   - the next step follows the failure: an expired session is told to sign
 *     in, a refusal to ask for access, anything else to try again.
 *
 * Props:
 *   status    classifyLoad() outcome: 'error' (default) | 'unauthenticated'
 *             | 'forbidden'. Picks the default title and the hint.
 *   title     page-specific headline ("Couldn't load projects"); falls back
 *             to a generic one for the status.
 *   onRetry   () => void | Promise. Omit to render without a button.
 *   variant   'panel' — a standalone bordered card (the page body is gone);
 *             'inset' — inside a panel that is already drawn (a table slot).
 *   testId / retryTestId  kept per page so existing tests keep their hooks.
 */
const HfLoadError = ({
  status = 'error',
  title,
  onRetry,
  variant = 'panel',
  testId,
  retryTestId,
  style,
}) => {
  const { t } = useTranslation();
  const [busy, setBusy] = useState(false);
  const mounted = useRef(true);
  useEffect(
    () => () => {
      mounted.current = false;
    },
    [],
  );

  const heading =
    title ??
    (status === 'unauthenticated'
      ? t(
          'console.load_error.title_unauthenticated',
          'Your session has expired',
        )
      : status === 'forbidden'
        ? t(
            'console.load_error.title_forbidden',
            'You don’t have access to this data',
          )
        : t('console.load_error.title', 'Couldn’t load this data'));
  const hint =
    status === 'unauthenticated'
      ? t(
          'console.load_error.detail_unauthenticated',
          'Sign in again, then retry.',
        )
      : status === 'forbidden'
        ? t(
            'console.load_error.detail_forbidden',
            'Ask an administrator for access.',
          )
        : t(
            'console.load_error.detail',
            'The server didn’t return this data, so nothing here is shown as empty. Try again in a moment.',
          );

  const retry = async () => {
    if (busy || !onRetry) return;
    setBusy(true);
    try {
      await onRetry();
    } catch {
      // The page renders the outcome of its own read; a throw here would
      // only surface as an unhandled rejection from a click handler.
    } finally {
      // A successful retry usually unmounts this panel.
      if (mounted.current) setBusy(false);
    }
  };

  return (
    <div
      role='alert'
      data-testid={testId}
      className={`hf-load-error ${variant === 'inset' ? 'is-inset' : 'is-panel'}`}
      style={style}
    >
      <svg
        className='hf-load-error__icon'
        viewBox='0 0 20 20'
        aria-hidden='true'
        focusable='false'
      >
        <circle
          cx='10'
          cy='10'
          r='8.25'
          fill='none'
          stroke='currentColor'
          strokeWidth='1.5'
        />
        <path
          d='M10 5.5v5.25'
          stroke='currentColor'
          strokeWidth='1.75'
          strokeLinecap='round'
        />
        <circle cx='10' cy='13.9' r='1.05' fill='currentColor' />
      </svg>
      <div className='hf-load-error__body'>
        <div className='hf-load-error__title'>{heading}</div>
        <div className='hf-load-error__detail'>{hint}</div>
      </div>
      {onRetry && (
        <button
          type='button'
          className='btn hf-load-error__action'
          data-testid={retryTestId}
          onClick={retry}
          disabled={busy}
          aria-busy={busy}
        >
          {busy
            ? t('console.load_error.retrying', 'Retrying…')
            : t('console.common.retry', 'retry')}
        </button>
      )}
    </div>
  );
};

export default HfLoadError;
