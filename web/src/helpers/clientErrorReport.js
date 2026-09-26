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

// Reports console errors from people's browsers to POST /api/client-error,
// where they become lurus_gateway_client_errors_total{kind} and a log line
// with page, release and request id. Before this a crash was visible only in
// the visitor's own devtools.
//
// Deliberately small: navigator.sendBeacon (survives the page unloading,
// never blocks the UI, needs no auth), a per-page-load cap so a render loop
// cannot flood the endpoint, and the page PATH only — never the query or
// hash, which can carry tokens (/bridge-login#t=...).

const ENDPOINT = '/api/client-error';
export const MAX_REPORTS_PER_LOAD = 5;
let sent = 0;

const baseURL = () => import.meta.env.VITE_REACT_APP_SERVER_URL || '';

export function reportClientError(kind, error) {
  if (typeof window === 'undefined' || sent >= MAX_REPORTS_PER_LOAD) return;
  sent += 1;
  const body = JSON.stringify({
    kind,
    page: window.location.pathname,
    message: String(error?.message ?? error ?? '').slice(0, 300),
    version: import.meta.env.VITE_REACT_APP_VERSION || '',
  });
  try {
    const blob = new Blob([body], { type: 'application/json' });
    if (navigator.sendBeacon?.(baseURL() + ENDPOINT, blob)) return;
  } catch (_) {
    // fall through to fetch
  }
  try {
    fetch(baseURL() + ENDPOINT, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body,
      keepalive: true,
    }).catch(() => {});
  } catch (_) {
    // Reporting must never throw into the page it is reporting on.
  }
}

export function installClientErrorReporting() {
  if (typeof window === 'undefined') return;
  window.addEventListener('error', (e) =>
    reportClientError('window_error', e.error || e.message),
  );
  window.addEventListener('unhandledrejection', (e) =>
    reportClientError('unhandled_rejection', e.reason),
  );
}

// Test seam: the cap is per page load, and a test file is one page load.
export function __resetClientErrorReportsForTest() {
  sent = 0;
}
