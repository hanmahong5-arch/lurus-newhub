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

// cycle-13 L7 (console honesty). App.jsx code-splits every routed v2 page
// into its own hash-named chunk (46 lazy() calls). Neither a synchronous
// throw inside one of those pages nor a lazy() import that rejects — the
// latter happens for real after every rolling release, while a browser tab
// left open still references chunk hashes the new deployment no longer
// serves — had anything above it to catch them: React unmounts the whole
// tree back to the nearest error boundary, and with none in the app there
// was none, so the visitor got a blank white screen with no way back short
// of knowing to hit reload themselves.
//
// Two independent mechanisms live here because they catch two different
// failures:
//   - ErrorBoundary (a class component — getDerivedStateFromError /
//     componentDidCatch have no hooks equivalent) catches a render-time
//     throw from anything below it and swaps in a panel with a reload
//     button, instead of leaving the shell blank.
//   - installPreloadErrorReload() listens for Vite's own
//     'vite:preloadError' window event, which fires when a dynamic
//     import() 404s — the exact stale-chunk case above — and self-heals by
//     reloading once. sessionStorage (not a module-level flag) guards the
//     "once": a plain in-memory guard resets on the reload itself, so if
//     the reload does not actually fix the fetch (the CDN is still serving
//     the old index.html mid-rollout, say) an in-memory guard would loop
//     the tab forever. A session flag survives the reload and stops after
//     one attempt either way.

import React from 'react';
import i18next from 'i18next';
import { reportClientError } from '../../helpers/clientErrorReport';

const PRELOAD_ERROR_RELOAD_KEY = 'lurus_preload_error_reloaded';

/**
 * Registers the 'vite:preloadError' self-heal exactly once per call. Meant
 * to be called once, near app startup (web/src/index.jsx) — it is not
 * idempotent against being called twice in the SAME session (two listeners
 * would each still only reload once, because of the sessionStorage guard,
 * but that is defense-in-depth, not the contract: call it once).
 */
export function installPreloadErrorReload() {
  if (typeof window === 'undefined') return;
  window.addEventListener('vite:preloadError', (e) => {
    reportClientError('chunk_load', e?.payload);
    try {
      if (window.sessionStorage.getItem(PRELOAD_ERROR_RELOAD_KEY)) {
        // Already tried once this session. A second stale-chunk error after
        // that reload means reloading did not fix it — looping would just
        // flash the tab forever instead of leaving the (still broken, but
        // at least stable) page an operator can screenshot and report.
        return;
      }
      window.sessionStorage.setItem(PRELOAD_ERROR_RELOAD_KEY, '1');
    } catch (_) {
      // Storage disabled/unavailable (locked-down browser policy) — fall
      // through and reload anyway; worst case is one extra reload with no
      // loop guard, not a silently-broken tab.
    }
    window.location.reload();
  });
}

class ErrorBoundary extends React.Component {
  constructor(props) {
    super(props);
    this.state = { hasError: false };
  }

  static getDerivedStateFromError() {
    return { hasError: true };
  }

  componentDidCatch(error, info) {
    reportClientError('render', error);
    // eslint-disable-next-line no-console
    console.error(
      'ErrorBoundary caught a render error:',
      error,
      info?.componentStack,
    );
  }

  handleReload = () => {
    window.location.reload();
  };

  render() {
    if (this.state.hasError) {
      return (
        <div
          role='alert'
          data-testid='error-boundary-panel'
          className='panel'
          style={{
            margin: 24,
            padding: '20px 24px',
            display: 'flex',
            alignItems: 'center',
            justifyContent: 'space-between',
            gap: 16,
            flexWrap: 'wrap',
          }}
        >
          <span className='strong'>
            {i18next.t('console.common.error', 'Something went wrong')}
          </span>
          <button
            type='button'
            className='btn'
            data-testid='error-boundary-reload'
            onClick={this.handleReload}
          >
            {i18next.t('console.common.retry', 'retry')}
          </button>
        </div>
      );
    }
    return this.props.children;
  }
}

export default ErrorBoundary;
