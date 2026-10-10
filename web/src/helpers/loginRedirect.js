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

// Where to send a person once they have signed in.
//
// The new console (web/next, served under /next/) has no sign-in screen of
// its own: an unauthenticated visit is sent to /login?redirect=<path> and this
// console's login flow brings them back. The value comes from the URL, so it
// is attacker-controlled; only a same-site relative path is accepted, anything
// else falls back to the default landing page.

export const DEFAULT_LANDING = '/console/v2/dashboard';

// Accepts "/next/keys?x=1#y" and rejects everything that could leave the site:
// absolute URLs, protocol-relative "//host", backslash tricks ("/\host" is read
// as a double slash by browsers), control characters, and the sign-in routes
// themselves (a bounce back to /login would loop).
export function safeRedirectPath(raw) {
  if (typeof raw !== 'string' || raw === '' || raw.length > 2048) return null;
  if (raw[0] !== '/' || raw[1] === '/' || raw[1] === '\\') return null;
  // eslint-disable-next-line no-control-regex
  if (/[\u0000-\u001f\u007f\\]/.test(raw)) return null;
  let url;
  try {
    url = new URL(raw, 'http://redirect.invalid');
  } catch (_) {
    return null;
  }
  if (url.origin !== 'http://redirect.invalid') return null;
  if (/^\/(login|bridge-login|register)(\/|$)/.test(url.pathname)) return null;
  return url.pathname + url.search + url.hash;
}

// The ?redirect= value on the current URL, validated; null when absent/unsafe.
export function readRedirectParam(search = window.location.search) {
  try {
    return safeRedirectPath(new URLSearchParams(search).get('redirect'));
  } catch (_) {
    return null;
  }
}

// Absolute URL to land on after sign-in.
export function postLoginUrl(origin = window.location.origin) {
  return origin + (readRedirectParam() || DEFAULT_LANDING);
}
