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
import { describe, it, expect } from 'vitest';
import { renderHook } from '@testing-library/react';

import { useNavigation } from './useNavigation';

const t = (key) => key;
const keysOf = (links) => links.map((l) => l.itemKey);

describe('useNavigation', () => {
  it('shows every module when no config is supplied', () => {
    const { result } = renderHook(() =>
      useNavigation(t, 'https://docs.example', undefined),
    );

    expect(keysOf(result.current.mainNavLinks)).toEqual([
      'home',
      'console',
      'pricing',
      'docs',
      'download',
    ]);
  });

  it('drops the docs entry when no docs link is configured', () => {
    const { result } = renderHook(() => useNavigation(t, '', undefined));
    expect(keysOf(result.current.mainNavLinks)).not.toContain('docs');
  });

  it('drops docs when the link exists but the module is switched off', () => {
    const { result } = renderHook(() =>
      useNavigation(t, 'https://docs.example', {
        home: true,
        console: true,
        pricing: true,
        docs: false,
        download: true,
      }),
    );
    expect(keysOf(result.current.mainNavLinks)).toEqual([
      'home',
      'console',
      'pricing',
      'download',
    ]);
  });

  it('hides modules that are absent from the config object', () => {
    // An explicit config is a whitelist: anything unlisted is hidden.
    const { result } = renderHook(() => useNavigation(t, '', { home: true }));
    expect(keysOf(result.current.mainNavLinks)).toEqual(['home']);
  });

  it('requires a literal true — truthy strings do not enable a module', () => {
    const { result } = renderHook(() =>
      useNavigation(t, '', { home: 'yes', console: 1, download: true }),
    );
    expect(keysOf(result.current.mainNavLinks)).toEqual(['download']);
  });

  it('reads pricing.enabled when pricing is an object', () => {
    const shown = renderHook(() =>
      useNavigation(t, '', { pricing: { enabled: true } }),
    );
    expect(keysOf(shown.result.current.mainNavLinks)).toEqual(['pricing']);

    const hidden = renderHook(() =>
      useNavigation(t, '', { pricing: { enabled: false } }),
    );
    expect(keysOf(hidden.result.current.mainNavLinks)).toEqual([]);
  });

  it('marks the docs entry external and carries the target url', () => {
    const { result } = renderHook(() =>
      useNavigation(t, 'https://docs.example/guide', undefined),
    );
    const docs = result.current.mainNavLinks.find((l) => l.itemKey === 'docs');

    expect(docs.isExternal).toBe(true);
    expect(docs.externalLink).toBe('https://docs.example/guide');
    expect(docs.to).toBeUndefined();
  });

  it('routes the internal entries to their console paths', () => {
    const { result } = renderHook(() => useNavigation(t, '', undefined));
    const byKey = Object.fromEntries(
      result.current.mainNavLinks.map((l) => [l.itemKey, l.to]),
    );

    expect(byKey).toEqual({
      home: '/',
      console: '/console',
      pricing: '/pricing',
      download: '/about',
    });
  });

  it('keeps the same array identity until an input changes', () => {
    const modules = { home: true };
    const { result, rerender } = renderHook(
      ({ docs, mods }) => useNavigation(t, docs, mods),
      { initialProps: { docs: '', mods: modules } },
    );

    const first = result.current.mainNavLinks;
    rerender({ docs: '', mods: modules });
    expect(result.current.mainNavLinks).toBe(first);

    rerender({ docs: 'https://docs.example', mods: modules });
    expect(result.current.mainNavLinks).not.toBe(first);
  });
});
