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
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { render, screen, waitFor } from '@testing-library/react';
import { readFileSync } from 'fs';
import { dirname, join } from 'path';
import { fileURLToPath } from 'url';

// Deterministic i18n: i18next.t(key, defaultValue) just returns the default
// (exactly how the real library resolves an untranslated-but-defaulted key),
// so assertions read the same English copy ErrorBoundary.jsx ships.
vi.mock('i18next', () => ({
  default: { t: (key, defaultValue) => defaultValue ?? key },
}));

vi.mock('../../helpers/clientErrorReport', () => ({
  reportClientError: vi.fn(),
}));

import ErrorBoundary, { installPreloadErrorReload } from './ErrorBoundary';

// React logs a caught error to the console itself (independent of
// componentDidCatch), which is expected noise here, not a signal — this
// file asserts on OUR explicit console.error call, not on absence of any.
const Boom = () => {
  throw new Error('render exploded');
};

const Fine = () => <div data-testid='fine'>still here</div>;

describe('ErrorBoundary — catching a synchronous render throw', () => {
  let errorSpy;

  beforeEach(() => {
    errorSpy = vi.spyOn(console, 'error').mockImplementation(() => {});
  });

  afterEach(() => {
    errorSpy.mockRestore();
  });

  it('renders children unchanged when nothing throws', () => {
    render(
      <ErrorBoundary>
        <Fine />
      </ErrorBoundary>,
    );
    expect(screen.getByTestId('fine')).toBeTruthy();
    expect(screen.queryByTestId('error-boundary-panel')).toBeNull();
  });

  it('renders a non-empty panel with a reload control instead of unmounting to blank', () => {
    render(
      <ErrorBoundary>
        <Boom />
      </ErrorBoundary>,
    );
    const panel = screen.getByTestId('error-boundary-panel');
    expect(panel).toBeTruthy();
    // "body non-empty": the boundary must leave SOMETHING on screen, not an
    // empty shell that merely avoided crashing the whole document.
    expect(panel.textContent.trim().length).toBeGreaterThan(0);
    expect(screen.getByTestId('error-boundary-reload')).toBeTruthy();
  });

  it('logs the caught error via console.error', () => {
    render(
      <ErrorBoundary>
        <Boom />
      </ErrorBoundary>,
    );
    const ownCall = errorSpy.mock.calls.find((c) =>
      String(c[0]).includes('ErrorBoundary caught a render error'),
    );
    expect(ownCall).toBeTruthy();
  });

  it('reports the caught error to the server, not only to the console', async () => {
    const { reportClientError } = await import(
      '../../helpers/clientErrorReport'
    );
    reportClientError.mockClear();
    render(
      <ErrorBoundary>
        <Boom />
      </ErrorBoundary>,
    );
    expect(reportClientError).toHaveBeenCalledWith(
      'render',
      expect.objectContaining({ message: 'render exploded' }),
    );
  });

  it('reloads the page when the reload control is clicked', () => {
    const reloadSpy = vi.fn();
    const originalLocation = window.location;
    Object.defineProperty(window, 'location', {
      configurable: true,
      value: { ...originalLocation, reload: reloadSpy },
    });
    try {
      render(
        <ErrorBoundary>
          <Boom />
        </ErrorBoundary>,
      );
      screen.getByTestId('error-boundary-reload').click();
      expect(reloadSpy).toHaveBeenCalledTimes(1);
    } finally {
      Object.defineProperty(window, 'location', {
        configurable: true,
        value: originalLocation,
      });
    }
  });

  // A boundary placed around only the failing subtree must not take the
  // rest of the shell down with it — the whole point of putting one boundary
  // PER route Suspense (App.jsx, W lane) rather than a single boundary at
  // the app root.
  it('contains a throw to its own subtree — a sibling outside the boundary still renders', () => {
    render(
      <div>
        <Fine />
        <ErrorBoundary>
          <Boom />
        </ErrorBoundary>
      </div>,
    );
    expect(screen.getByTestId('fine')).toBeTruthy();
    expect(screen.getByTestId('error-boundary-panel')).toBeTruthy();
  });

  // A React.lazy() import that rejects — the real-world stale-chunk failure
  // this component exists for — throws during render exactly like a
  // synchronous throw does once the rejected promise settles; this is that
  // path, not the synchronous-throw stand-in above.
  it('catches a rejected lazy() import the same way it catches a synchronous throw', async () => {
    const LazyBoom = React.lazy(() => Promise.reject(new Error('chunk 404')));
    render(
      <ErrorBoundary>
        <React.Suspense fallback={<div data-testid='loading'>...</div>}>
          <LazyBoom />
        </React.Suspense>
      </ErrorBoundary>,
    );
    await waitFor(() => {
      const panel = screen.getByTestId('error-boundary-panel');
      expect(panel.textContent.trim().length).toBeGreaterThan(0);
    });
  });
});

describe('installPreloadErrorReload', () => {
  // jsdom's `window` is shared across every `it()` in this file, so a
  // listener installed by one test and never removed would still be live —
  // and firing — for every test after it. addEventListener is spied so each
  // test can capture exactly the handler IT registered and remove only
  // that one afterward, instead of assuming a clean `window` per test.
  let addSpy;
  const installedHandlers = [];

  const install = () => {
    installPreloadErrorReload();
    const call = addSpy.mock.calls.find(
      ([type]) => type === 'vite:preloadError',
    );
    installedHandlers.push(call[1]);
    return call[1];
  };

  const dispatchPreloadError = () => {
    window.dispatchEvent(new Event('vite:preloadError'));
  };

  const stubReload = () => {
    const reloadSpy = vi.fn();
    const originalLocation = window.location;
    Object.defineProperty(window, 'location', {
      configurable: true,
      value: { ...originalLocation, reload: reloadSpy },
    });
    return {
      reloadSpy,
      restore: () =>
        Object.defineProperty(window, 'location', {
          configurable: true,
          value: originalLocation,
        }),
    };
  };

  beforeEach(() => {
    window.sessionStorage.clear();
    addSpy = vi.spyOn(window, 'addEventListener');
  });

  afterEach(() => {
    for (const handler of installedHandlers.splice(0)) {
      window.removeEventListener('vite:preloadError', handler);
    }
    addSpy.mockRestore();
    window.sessionStorage.clear();
  });

  it('reloads once when vite:preloadError fires', () => {
    install();
    const { reloadSpy, restore } = stubReload();
    try {
      dispatchPreloadError();
      expect(reloadSpy).toHaveBeenCalledTimes(1);
    } finally {
      restore();
    }
  });

  // The event can fire more than once (two different lazy chunks failing in
  // the same broken deployment) — only the first may trigger a reload, or a
  // reload that does not fix the fetch would loop the tab forever.
  it('does not reload a second time when the event fires again in the same session', () => {
    install();
    const { reloadSpy, restore } = stubReload();
    try {
      dispatchPreloadError();
      dispatchPreloadError();
      expect(reloadSpy).toHaveBeenCalledTimes(1);
    } finally {
      restore();
    }
  });

  it('sets the sessionStorage guard so a later page load (post-reload) also skips', () => {
    install();
    const { reloadSpy, restore } = stubReload();
    try {
      dispatchPreloadError();
      expect(
        window.sessionStorage.getItem('lurus_preload_error_reloaded'),
      ).toBe('1');
      // Simulate a second module evaluation after the reload (a fresh JS
      // heap, but the SAME session storage) re-registering the listener.
      install();
      dispatchPreloadError();
      expect(reloadSpy).toHaveBeenCalledTimes(1);
    } finally {
      restore();
    }
  });
});

// ─── wiring: the boundary has to be INSTALLED, not merely written ────────────
//
// web/src/index.jsx calls ReactDOM.createRoot at module scope (documented at
// index.jsx:33-36), so no test can import it and observe the real tree. The
// repo already reads that file as text for the same reason
// (src/z9_entry_icon_barrel.test.js walks its static import graph), so this
// does too: nothing else fails if the wrap or the preload self-heal is
// deleted from the entry, which would leave this component correct and
// unreachable.
describe('entry wiring (web/src/index.jsx read as text)', () => {
  const HERE = dirname(fileURLToPath(import.meta.url));
  const ENTRY = join(HERE, '..', '..', 'index.jsx');
  const source = readFileSync(ENTRY, 'utf8');

  it('imports the boundary and its preload self-heal from this module', () => {
    expect(source).toMatch(
      /import\s+ErrorBoundary\s*,\s*\{[\s\S]*?installPreloadErrorReload[\s\S]*?\}\s*from\s*'\.\/components\/common\/ErrorBoundary'/,
    );
  });

  it('calls installPreloadErrorReload() at startup', () => {
    expect(source).toMatch(/^installPreloadErrorReload\(\);$/m);
  });

  it('renders <PageLayout /> inside <ErrorBoundary>', () => {
    const wrapped = source.match(/<ErrorBoundary>([\s\S]*?)<\/ErrorBoundary>/);
    expect(wrapped, 'no <ErrorBoundary> element in index.jsx').toBeTruthy();
    expect(wrapped[1]).toMatch(/<PageLayout/);
  });
});
