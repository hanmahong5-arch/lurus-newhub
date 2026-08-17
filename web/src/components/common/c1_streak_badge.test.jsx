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
import { act, render, screen } from '@testing-library/react';

// Semi UI is unimportable under jsdom (canvas). Tooltip is the only piece
// StreakBadge uses; render its content inline so the tooltip copy — which is
// where the streak progress text lives — is assertable.
vi.mock('@douyinfe/semi-ui', () => ({
  Tooltip: ({ content, children }) =>
    React.createElement(
      'div',
      null,
      React.createElement('div', { 'data-testid': 'tooltip-content' }, content),
      children,
    ),
}));

import StreakBadge from './StreakBadge';

const STORAGE_KEY = 'lurus-api:streak';

// Same local-date formatting the component uses.
const dayOffset = (days) => {
  const d = new Date();
  d.setDate(d.getDate() + days);
  const y = d.getFullYear();
  const m = String(d.getMonth() + 1).padStart(2, '0');
  const day = String(d.getDate()).padStart(2, '0');
  return `${y}-${m}-${day}`;
};

const seed = (state) =>
  localStorage.setItem(STORAGE_KEY, JSON.stringify(state));
const stored = () => JSON.parse(localStorage.getItem(STORAGE_KEY));
const badge = () => screen.getByRole('button');
const tooltip = () => screen.getByTestId('tooltip-content').textContent;

beforeEach(() => {
  localStorage.clear();
  document.getElementById('streak-badge-styles')?.remove();
});

describe('StreakBadge — streak accounting', () => {
  it('starts a first-time visitor at day 1 and persists it', () => {
    render(<StreakBadge />);

    expect(badge()).toHaveAttribute('aria-label', '连续签到 1 天');
    expect(stored()).toEqual({
      currentStreak: 1,
      lastActiveDate: dayOffset(0),
      longestStreak: 1,
      rewards: [],
    });
  });

  it('is idempotent within the same day', () => {
    seed({
      currentStreak: 4,
      lastActiveDate: dayOffset(0),
      longestStreak: 9,
      rewards: [],
    });

    render(<StreakBadge />);

    expect(badge()).toHaveAttribute('aria-label', '连续签到 4 天');
    expect(stored().currentStreak).toBe(4);
    expect(stored().longestStreak).toBe(9);
  });

  it('increments on a consecutive day and raises the personal best', () => {
    seed({
      currentStreak: 4,
      lastActiveDate: dayOffset(-1),
      longestStreak: 4,
      rewards: [],
    });

    render(<StreakBadge />);

    expect(badge()).toHaveAttribute('aria-label', '连续签到 5 天');
    expect(stored()).toMatchObject({
      currentStreak: 5,
      longestStreak: 5,
      lastActiveDate: dayOffset(0),
    });
  });

  it('resets to 1 after a missed day but keeps the personal best', () => {
    seed({
      currentStreak: 9,
      lastActiveDate: dayOffset(-3),
      longestStreak: 9,
      rewards: [],
    });

    render(<StreakBadge />);

    expect(badge()).toHaveAttribute('aria-label', '连续签到 1 天');
    expect(stored().currentStreak).toBe(1);
    expect(stored().longestStreak).toBe(9);
    expect(tooltip()).toContain('历史最长');
    expect(tooltip()).toContain('9');
  });

  it('does not show a personal-best line while the current run is the best', () => {
    render(<StreakBadge />);

    expect(tooltip()).not.toContain('历史最长');
  });

  it('treats a clock jumped backwards (future last-active date) as a new run', () => {
    seed({
      currentStreak: 12,
      lastActiveDate: dayOffset(5),
      longestStreak: 12,
      rewards: [],
    });

    render(<StreakBadge />);

    expect(badge()).toHaveAttribute('aria-label', '连续签到 1 天');
  });
});

describe('StreakBadge — corrupt storage', () => {
  it('ignores non-JSON storage instead of crashing', () => {
    localStorage.setItem(STORAGE_KEY, 'not-json-at-all');

    render(<StreakBadge />);

    expect(badge()).toHaveAttribute('aria-label', '连续签到 1 天');
    expect(stored().currentStreak).toBe(1);
  });

  it('survives a storage backend that throws on write', () => {
    const setItem = vi
      .spyOn(Storage.prototype, 'setItem')
      .mockImplementation(() => {
        throw new Error('QuotaExceededError');
      });

    // Persistence fails but the badge must still render this session's streak.
    expect(() => render(<StreakBadge />)).not.toThrow();
    expect(badge()).toHaveAttribute('aria-label', '连续签到 1 天');

    setItem.mockRestore();
  });

  // DEFECT (skipped): recordActivity() trusts whatever shape came out of
  // localStorage. A record written by an older build — or hand-edited — that
  // lacks `rewards` blows up with "Cannot read properties of undefined
  // (reading 'includes')" the moment the streak lands on a milestone, and the
  // throw happens inside a mount effect, so the surrounding page goes down
  // with it. Storage is user-writable; it needs shape validation.
  it.skip('tolerates a legacy record with no rewards array', () => {
    seed({ currentStreak: 6, lastActiveDate: dayOffset(-1) });

    render(<StreakBadge />);

    expect(badge()).toHaveAttribute('aria-label', '连续签到 7 天');
  });
});

describe('StreakBadge — milestones', () => {
  beforeEach(() => {
    vi.useFakeTimers();
  });
  afterEach(() => {
    vi.useRealTimers();
  });

  it('celebrates on day 7, records the reward, then settles down', () => {
    seed({
      currentStreak: 6,
      lastActiveDate: dayOffset(-1),
      longestStreak: 6,
      rewards: [],
    });

    render(<StreakBadge />);

    expect(badge().className).toBe('streak-celebrate-api');
    expect(document.querySelector('.streak-confetti-api')).not.toBeNull();
    expect(stored().rewards).toEqual([`7 天连续签到达成 (${dayOffset(0)})`]);

    act(() => vi.advanceTimersByTime(1500));

    expect(badge().className).toBe('');
    expect(document.querySelector('.streak-confetti-api')).toBeNull();
  });

  it('celebrates on day 30 and records the 30-day reward', () => {
    seed({
      currentStreak: 29,
      lastActiveDate: dayOffset(-1),
      longestStreak: 29,
      rewards: [`7 天连续签到达成 (${dayOffset(-23)})`],
    });

    render(<StreakBadge />);

    expect(badge().className).toBe('streak-celebrate-api');
    expect(stored().rewards).toHaveLength(2);
    expect(stored().rewards[1]).toBe(`30 天连续签到达成 (${dayOffset(0)})`);
  });

  it('does not celebrate on a non-milestone day', () => {
    seed({
      currentStreak: 3,
      lastActiveDate: dayOffset(-1),
      longestStreak: 3,
      rewards: [],
    });

    render(<StreakBadge />);

    expect(badge().className).toBe('');
    expect(stored().rewards).toEqual([]);
  });
});

describe('StreakBadge — progress copy', () => {
  it('counts down to the 7-day milestone below day 7', () => {
    seed({
      currentStreak: 1,
      lastActiveDate: dayOffset(-1),
      longestStreak: 1,
      rewards: [],
    });

    render(<StreakBadge />);

    expect(tooltip()).toContain('再坚持 5 天解锁 7 天成就');
  });

  it('counts down to the 30-day milestone between day 7 and 29', () => {
    seed({
      currentStreak: 9,
      lastActiveDate: dayOffset(-1),
      longestStreak: 9,
      rewards: [],
    });

    render(<StreakBadge />);

    expect(tooltip()).toContain('再坚持 20 天解锁 30 天大奖');
  });

  it('reports every milestone unlocked from day 30 on', () => {
    seed({
      currentStreak: 40,
      lastActiveDate: dayOffset(0),
      longestStreak: 40,
      rewards: [],
    });

    render(<StreakBadge />);

    expect(tooltip()).toContain('已解锁全部里程碑');
  });
});

describe('StreakBadge — style injection', () => {
  it('injects the keyframe stylesheet exactly once across mounts', () => {
    const { unmount } = render(<StreakBadge />);
    expect(document.querySelectorAll('style#streak-badge-styles')).toHaveLength(
      1,
    );
    unmount();

    render(<StreakBadge />);

    expect(document.querySelectorAll('style#streak-badge-styles')).toHaveLength(
      1,
    );
    expect(
      document.getElementById('streak-badge-styles').textContent,
    ).toContain('@keyframes streak-bounce');
  });
});
