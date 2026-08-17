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
import { describe, it, expect, beforeEach } from 'vitest';
import { renderHook, act } from '@testing-library/react';

import { useNotifications } from './useNotifications';

const READ_KEY = 'notice_read_keys';

const statusWith = (announcements) => ({ status: { announcements } });

const announcement = (publishDate, content) => ({ publishDate, content });

beforeEach(() => {
  window.localStorage.clear();
});

describe('useNotifications — unread accounting', () => {
  it('counts every announcement as unread on a fresh browser', () => {
    const { result } = renderHook(() =>
      useNotifications(
        statusWith([
          announcement('2026-01-01', 'a'),
          announcement('2026-01-02', 'b'),
        ]),
      ),
    );

    expect(result.current.unreadCount).toBe(2);
    expect(result.current.noticeVisible).toBe(false);
  });

  it('reports zero when the status payload carries no announcements', () => {
    const { result } = renderHook(() => useNotifications(undefined));

    expect(result.current.announcements).toEqual([]);
    expect(result.current.unreadCount).toBe(0);
    expect(result.current.getUnreadKeys()).toEqual([]);
  });

  it('excludes announcements whose key is already in the read set', () => {
    window.localStorage.setItem(READ_KEY, JSON.stringify(['2026-01-01-a']));

    const { result } = renderHook(() =>
      useNotifications(
        statusWith([
          announcement('2026-01-01', 'a'),
          announcement('2026-01-02', 'b'),
        ]),
      ),
    );

    expect(result.current.unreadCount).toBe(1);
    expect(result.current.getUnreadKeys()).toEqual(['2026-01-02-b']);
  });

  it('keys an announcement by publish date plus the first 30 content chars', () => {
    const long = 'x'.repeat(40);
    const { result } = renderHook(() =>
      useNotifications(statusWith([announcement('2026-02-02', long)])),
    );

    expect(result.current.getUnreadKeys()).toEqual([
      `2026-02-02-${'x'.repeat(30)}`,
    ]);
  });

  it('collides two announcements that share date and first 30 chars', () => {
    // Documented consequence of the truncating key: an edited notice whose
    // first 30 characters are unchanged is treated as already read.
    const head = 'y'.repeat(30);
    window.localStorage.setItem(
      READ_KEY,
      JSON.stringify([`2026-03-03-${head}`]),
    );

    const { result } = renderHook(() =>
      useNotifications(
        statusWith([announcement('2026-03-03', head + ' brand new tail')]),
      ),
    );

    expect(result.current.unreadCount).toBe(0);
  });

  it('tolerates announcements missing date or content', () => {
    const { result } = renderHook(() =>
      useNotifications(statusWith([{}, { content: 'only-content' }])),
    );

    expect(result.current.unreadCount).toBe(2);
    expect(result.current.getUnreadKeys()).toEqual(['-', '-only-content']);
  });

  it('treats a corrupt read-keys blob as an empty read set', () => {
    window.localStorage.setItem(READ_KEY, '}}not json');

    const { result } = renderHook(() =>
      useNotifications(statusWith([announcement('2026-01-01', 'a')])),
    );

    expect(result.current.unreadCount).toBe(1);
  });
});

describe('useNotifications — open/close lifecycle', () => {
  it('opens the drawer without marking anything read', () => {
    const { result } = renderHook(() =>
      useNotifications(statusWith([announcement('2026-01-01', 'a')])),
    );

    act(() => result.current.handleNoticeOpen());

    expect(result.current.noticeVisible).toBe(true);
    expect(result.current.unreadCount).toBe(1);
    expect(window.localStorage.getItem(READ_KEY)).toBeNull();
  });

  it('closing persists every current key and zeroes the badge', () => {
    const { result } = renderHook(() =>
      useNotifications(
        statusWith([
          announcement('2026-01-01', 'a'),
          announcement('2026-01-02', 'b'),
        ]),
      ),
    );

    act(() => result.current.handleNoticeOpen());
    act(() => result.current.handleNoticeClose());

    expect(result.current.noticeVisible).toBe(false);
    expect(result.current.unreadCount).toBe(0);
    expect(JSON.parse(window.localStorage.getItem(READ_KEY))).toEqual([
      '2026-01-01-a',
      '2026-01-02-b',
    ]);
  });

  it('merges into the existing read set without duplicating keys', () => {
    window.localStorage.setItem(
      READ_KEY,
      JSON.stringify(['2026-01-01-a', 'legacy-key']),
    );

    const { result } = renderHook(() =>
      useNotifications(
        statusWith([
          announcement('2026-01-01', 'a'),
          announcement('2026-01-02', 'b'),
        ]),
      ),
    );

    act(() => result.current.handleNoticeClose());

    expect(JSON.parse(window.localStorage.getItem(READ_KEY))).toEqual([
      '2026-01-01-a',
      'legacy-key',
      '2026-01-02-b',
    ]);
  });

  it('does not write a read-keys entry when there is nothing to read', () => {
    const { result } = renderHook(() => useNotifications(statusWith([])));

    act(() => result.current.handleNoticeClose());

    expect(window.localStorage.getItem(READ_KEY)).toBeNull();
    expect(result.current.unreadCount).toBe(0);
  });

  it('a newly published announcement re-arms the badge after a close', () => {
    const first = announcement('2026-01-01', 'a');
    const { result, rerender } = renderHook(
      ({ list }) => useNotifications(statusWith(list)),
      { initialProps: { list: [first] } },
    );

    act(() => result.current.handleNoticeClose());
    expect(result.current.unreadCount).toBe(0);

    rerender({ list: [first, announcement('2026-01-05', 'fresh')] });
    expect(result.current.unreadCount).toBe(1);
  });
});
