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
import { describe, expect, it } from 'vitest';
import {
  DEFAULT_LANDING,
  postLoginUrl,
  readRedirectParam,
  safeRedirectPath,
} from './loginRedirect';

describe('safeRedirectPath', () => {
  it('accepts in-site relative paths with query and hash', () => {
    expect(safeRedirectPath('/next/keys')).toBe('/next/keys');
    expect(safeRedirectPath('/next/usage-logs?p=2#top')).toBe(
      '/next/usage-logs?p=2#top',
    );
    expect(safeRedirectPath('/console/v2/token')).toBe('/console/v2/token');
  });

  it.each([
    ['absolute url', 'https://evil.example/next'],
    ['protocol-relative', '//evil.example/next'],
    ['backslash host', '/\\evil.example'],
    ['embedded backslash', '/next\\..\\x'],
    ['no leading slash', 'next/keys'],
    ['javascript scheme', 'javascript:alert(1)'],
    ['control character', '/next/\u0000keys'],
    ['newline', '/next/\nkeys'],
    ['login loop', '/login?redirect=/next/'],
    ['bridge loop', '/bridge-login'],
    ['empty', ''],
    ['non-string', null],
  ])('rejects %s', (_name, raw) => {
    expect(safeRedirectPath(raw)).toBeNull();
  });
});

describe('readRedirectParam / postLoginUrl', () => {
  it('reads and validates ?redirect=', () => {
    expect(readRedirectParam('?redirect=%2Fnext%2Fkeys')).toBe('/next/keys');
    expect(
      readRedirectParam('?redirect=https%3A%2F%2Fevil.example'),
    ).toBeNull();
    expect(readRedirectParam('')).toBeNull();
  });

  it('falls back to the default landing page', () => {
    window.history.replaceState(null, '', '/login');
    expect(postLoginUrl('https://hub.example')).toBe(
      `https://hub.example${DEFAULT_LANDING}`,
    );
    window.history.replaceState(null, '', '/login?redirect=%2Fnext%2Fmodels');
    expect(postLoginUrl('https://hub.example')).toBe(
      'https://hub.example/next/models',
    );
    window.history.replaceState(null, '', '/');
  });
});
