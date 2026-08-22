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

import React, {
  useEffect,
  useState,
  useCallback,
  useRef,
  useMemo,
} from 'react';
import { Tooltip } from '@douyinfe/semi-ui';
import { useTranslation } from 'react-i18next';

// =============================================================================
// Constants
// =============================================================================

const STORAGE_KEY = 'lurus-api:streak';
const MILESTONE_7 = 7;
const MILESTONE_30 = 30;
const CELEBRATION_DURATION_MS = 1500;

// =============================================================================
// Streak Logic (pure functions)
// =============================================================================

/** Return today's date as YYYY-MM-DD in local timezone */
function getLocalDateString() {
  const now = new Date();
  const year = now.getFullYear();
  const month = String(now.getMonth() + 1).padStart(2, '0');
  const day = String(now.getDate()).padStart(2, '0');
  return `${year}-${month}-${day}`;
}

/** Check if dateB is exactly 1 calendar day after dateA */
function isConsecutiveDay(dateA, dateB) {
  const a = new Date(dateA + 'T00:00:00');
  const b = new Date(dateB + 'T00:00:00');
  const diffMs = b.getTime() - a.getTime();
  const diffDays = Math.round(diffMs / (24 * 60 * 60 * 1000));
  return diffDays === 1;
}

/** Load streak state from localStorage */
function loadStreak() {
  try {
    const raw = localStorage.getItem(STORAGE_KEY);
    if (!raw) return null;
    return JSON.parse(raw);
  } catch {
    return null;
  }
}

/** Save streak state to localStorage */
function saveStreak(state) {
  try {
    localStorage.setItem(STORAGE_KEY, JSON.stringify(state));
  } catch {
    // Quota exceeded or restricted environment — fail silently
  }
}

/** Record activity and return updated state */
function recordActivity(current) {
  const today = getLocalDateString();
  const state = current || {
    currentStreak: 0,
    lastActiveDate: '',
    longestStreak: 0,
    rewards: [],
  };

  // Already recorded today — no change
  if (state.lastActiveDate === today) return state;

  const updated = { ...state };

  if (isConsecutiveDay(state.lastActiveDate, today)) {
    updated.currentStreak = state.currentStreak + 1;
  } else {
    updated.currentStreak = 1;
  }

  updated.lastActiveDate = today;

  if (updated.currentStreak > updated.longestStreak) {
    updated.longestStreak = updated.currentStreak;
  }

  // Milestone rewards. The label is a stable identifier, never a display
  // string: it is persisted to localStorage, so a translated label would make
  // the dedupe below depend on whichever language happened to be active when
  // the milestone was reached.
  const milestones = [MILESTONE_7, MILESTONE_30];
  for (const threshold of milestones) {
    if (updated.currentStreak === threshold) {
      const label = `streak-${threshold}-${today}`;
      if (!updated.rewards.includes(label)) {
        updated.rewards = [...updated.rewards, label];
      }
    }
  }

  return updated;
}

// =============================================================================
// CSS (injected once)
// =============================================================================

const STYLE_ID = 'streak-badge-styles';

function ensureStyles() {
  if (typeof document === 'undefined') return;
  if (document.getElementById(STYLE_ID)) return;

  const style = document.createElement('style');
  style.id = STYLE_ID;
  style.textContent = `
    @keyframes streak-bounce {
      0%, 100% { transform: scale(1); }
      25% { transform: scale(1.3); }
      50% { transform: scale(0.95); }
      75% { transform: scale(1.15); }
    }
    @keyframes streak-particle {
      0% { opacity: 1; transform: translate(0, 0) scale(1); }
      100% { opacity: 0; transform: translate(var(--tx), var(--ty)) scale(0); }
    }
    .streak-celebrate-api { animation: streak-bounce 0.6s ease-in-out; }
    .streak-confetti-api {
      position: absolute; inset: 0; pointer-events: none;
    }
    .streak-confetti-api::before,
    .streak-confetti-api::after {
      content: ''; position: absolute; width: 4px; height: 4px;
      border-radius: 50%; animation: streak-particle 0.8s ease-out forwards;
    }
    .streak-confetti-api::before {
      background: #f59e0b; top: 0; left: 50%; --tx: -8px; --ty: -12px;
    }
    .streak-confetti-api::after {
      background: #ef4444; top: 0; right: 30%; --tx: 10px; --ty: -10px;
    }
    @media (prefers-reduced-motion: reduce) {
      .streak-celebrate-api { animation: none; }
      .streak-confetti-api::before,
      .streak-confetti-api::after { animation: none; }
    }
  `;
  document.head.appendChild(style);
}

// =============================================================================
// Component
// =============================================================================

const StreakBadge = () => {
  const { t } = useTranslation();
  const [streak, setStreak] = useState(null);
  const [celebrating, setCelebrating] = useState(false);
  const prevStreakRef = useRef(0);

  // Inject styles once
  useEffect(() => {
    ensureStyles();
  }, []);

  // Load and record on mount
  useEffect(() => {
    const current = loadStreak();
    const updated = recordActivity(current);
    saveStreak(updated);
    setStreak(updated);
  }, []);

  // Detect milestone celebration
  useEffect(() => {
    if (!streak) return;
    const prev = prevStreakRef.current;
    if (
      streak.currentStreak !== prev &&
      (streak.currentStreak === MILESTONE_7 ||
        streak.currentStreak === MILESTONE_30)
    ) {
      setCelebrating(true);
      const timer = setTimeout(
        () => setCelebrating(false),
        CELEBRATION_DURATION_MS,
      );
      prevStreakRef.current = streak.currentStreak;
      return () => clearTimeout(timer);
    }
    prevStreakRef.current = streak.currentStreak;
  }, [streak]);

  const progressMessage = useMemo(() => {
    if (!streak) return '';
    const s = streak.currentStreak;
    if (s < MILESTONE_7)
      return t('再坚持 {{days}} 天解锁 7 天成就', { days: MILESTONE_7 - s });
    if (s < MILESTONE_30)
      return t('再坚持 {{days}} 天解锁 30 天大奖', { days: MILESTONE_30 - s });
    return t('已解锁全部里程碑');
  }, [streak, t]);

  // Don't render until loaded, or if no activity yet
  if (!streak || streak.currentStreak <= 0) return null;

  const tooltipContent = (
    <div style={{ maxWidth: 200 }}>
      <div style={{ fontWeight: 600, marginBottom: 4 }}>
        {'🔥'} {t('连续签到 {{days}} 天', { days: streak.currentStreak })}
      </div>
      <div style={{ fontSize: 12, opacity: 0.7 }}>{progressMessage}</div>
      {streak.longestStreak > streak.currentStreak && (
        <div style={{ fontSize: 12, opacity: 0.5, marginTop: 2 }}>
          {t('历史最长')}: {streak.longestStreak} {t('天')}
        </div>
      )}
    </div>
  );

  return (
    <Tooltip content={tooltipContent} position='bottom'>
      <button
        className={celebrating ? 'streak-celebrate-api' : ''}
        style={{
          position: 'relative',
          display: 'inline-flex',
          alignItems: 'center',
          gap: 4,
          padding: '4px 8px',
          border: 'none',
          background: 'transparent',
          borderRadius: 6,
          cursor: 'default',
          transition: 'background 0.15s',
          fontSize: 14,
        }}
        aria-label={t('连续签到 {{days}} 天', { days: streak.currentStreak })}
        onMouseEnter={(e) => {
          e.currentTarget.style.background = 'rgba(var(--semi-grey-9), 0.08)';
        }}
        onMouseLeave={(e) => {
          e.currentTarget.style.background = 'transparent';
        }}
      >
        <span role='img' aria-hidden='true'>
          {'🔥'}
        </span>
        <span
          style={{ fontFamily: 'monospace', fontWeight: 500, fontSize: 13 }}
        >
          {streak.currentStreak}
        </span>
        {celebrating && (
          <span className='streak-confetti-api' aria-hidden='true' />
        )}
      </button>
    </Tooltip>
  );
};

export default StreakBadge;
