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
import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest';

// utils.jsx reads window.matchMedia at MODULE scope with no guard, so the
// import itself throws under jsdom unless a stub exists first.
vi.hoisted(() => {
  if (typeof window !== 'undefined' && !window.matchMedia) {
    window.matchMedia = (query) => ({
      matches: false,
      media: query,
      onchange: null,
      addListener: () => {},
      removeListener: () => {},
      addEventListener: () => {},
      removeEventListener: () => {},
      dispatchEvent: () => false,
    });
  }
});

// The Semi UI barrel drags in lottie-web, which needs a canvas jsdom does
// not provide. utils.jsx only uses Toast (imperative) and Pagination.
vi.mock('@douyinfe/semi-ui', () => ({
  Toast: {
    error: vi.fn(),
    warning: vi.fn(),
    success: vi.fn(),
    info: vi.fn(),
  },
  Pagination: (props) =>
    React.createElement('nav', {
      'data-testid': 'pagination',
      'data-current': String(props.currentPage),
      'data-page-size': String(props.pageSize),
      'data-total': String(props.total),
      'data-size': props.size,
    }),
}));

import {
  isAdmin,
  isRoot,
  isSubscriber,
  getUserRole,
  getRoleName,
  getSystemName,
  getLogo,
  getUserIdFromLocalStorage,
  getFooterHTML,
  removeTrailingSlash,
  getTodayStartTimestamp,
  timestamp2string,
  timestamp2string1,
  isDataCrossYear,
  verifyJSON,
  verifyJSONPromise,
  shouldShowPrompt,
  setPromptShown,
  compareObjects,
  generateMessageId,
  getTextContent,
  processThinkTags,
  processIncompleteThinkTags,
  buildMessageContent,
  createMessage,
  createLoadingAssistantMessage,
  hasImageContent,
  formatMessageForAPI,
  isValidMessage,
  getLastUserMessage,
  getLastAssistantMessage,
  getRelativeTime,
  formatDateString,
  formatDateTimeString,
  getTableCompactMode,
  setTableCompactMode,
  selectFilter,
  calculateModelPrice,
  resetPricingFilters,
} from './utils';

beforeEach(() => {
  localStorage.clear();
});

// ── role gates ──────────────────────────────────────────────────────────
describe('role predicates read the localStorage user shim', () => {
  const setUser = (role) =>
    localStorage.setItem('user', JSON.stringify({ role, id: 42 }));

  it('all predicates are false when no user is stored', () => {
    expect(isAdmin()).toBe(false);
    expect(isRoot()).toBe(false);
    expect(isSubscriber()).toBe(false);
    expect(getUserRole()).toBe(0);
  });

  it('a plain user (role 1) clears no gate', () => {
    setUser(1);
    expect(isSubscriber()).toBe(false);
    expect(isAdmin()).toBe(false);
    expect(isRoot()).toBe(false);
    expect(getUserRole()).toBe(1);
  });

  it('role 5 is a subscriber only', () => {
    setUser(5);
    expect(isSubscriber()).toBe(true);
    expect(isAdmin()).toBe(false);
    expect(isRoot()).toBe(false);
  });

  it('role 10 is admin and subscriber but not root', () => {
    setUser(10);
    expect(isSubscriber()).toBe(true);
    expect(isAdmin()).toBe(true);
    expect(isRoot()).toBe(false);
  });

  it('role 100 clears every gate', () => {
    setUser(100);
    expect(isSubscriber()).toBe(true);
    expect(isAdmin()).toBe(true);
    expect(isRoot()).toBe(true);
  });

  it('the gates are >= comparisons, so an unknown high role is still admin', () => {
    setUser(50);
    expect(isAdmin()).toBe(true);
    expect(isRoot()).toBe(false);
  });

  it('getUserRole coerces a missing role field to 0', () => {
    localStorage.setItem('user', JSON.stringify({ id: 7 }));
    expect(getUserRole()).toBe(0);
  });

  it('getUserIdFromLocalStorage returns -1 with no user', () => {
    expect(getUserIdFromLocalStorage()).toBe(-1);
    setUser(1);
    expect(getUserIdFromLocalStorage()).toBe(42);
  });
});

describe('getRoleName', () => {
  it('maps the five known role numbers', () => {
    expect(getRoleName(0)).toBe('Guest');
    expect(getRoleName(1)).toBe('User');
    expect(getRoleName(5)).toBe('Subscriber');
    expect(getRoleName(10)).toBe('Admin');
    expect(getRoleName(100)).toBe('Root');
  });

  it('falls back to Unknown for an unmapped role', () => {
    expect(getRoleName(7)).toBe('Unknown');
    expect(getRoleName(undefined)).toBe('Unknown');
  });

  it('should return Unknown for a role that collides with an Object key', () => {
    // DEFECT: `names[role] || 'Unknown'` (utils.jsx:90) indexes an object
    // literal, so the lookup walks Object.prototype. getRoleName('toString')
    // returns the inherited *function* rather than 'Unknown'. Any role value
    // that arrives as a string from an API response can trip this.
    expect(getRoleName('toString')).toBe('Unknown');
  });
});

describe('branding fallbacks', () => {
  it('getSystemName falls back when the key is absent', () => {
    expect(getSystemName()).toBe('Lurus API');
    localStorage.setItem('system_name', 'Acme Hub');
    expect(getSystemName()).toBe('Acme Hub');
  });

  it('getLogo falls back to the bundled asset when absent', () => {
    expect(getLogo()).toBe('/logo.png');
    localStorage.setItem('logo', '/brand/acme.svg');
    expect(getLogo()).toBe('/brand/acme.svg');
  });

  it('getFooterHTML returns null (not empty string) when unset', () => {
    expect(getFooterHTML()).toBeNull();
  });

  it('should still fall back when /api/status omitted the field', () => {
    // DEFECT: data.js setStatusData does localStorage.setItem('logo',
    // data.logo) unconditionally. localStorage stringifies, so a missing
    // field is persisted as the four characters "undefined" -- which is
    // truthy, so the `if (!logo)` fallback here never fires and the console
    // renders <img src="undefined">. Same for system_name and, worse, for
    // quota_per_unit (parseFloat("undefined") === NaN -> "$NaN" money).
    localStorage.setItem('logo', String(undefined));
    localStorage.setItem('system_name', String(undefined));
    expect(getLogo()).toBe('/logo.png');
    expect(getSystemName()).toBe('Lurus API');
  });
});

describe('removeTrailingSlash', () => {
  it('drops exactly one trailing slash', () => {
    expect(removeTrailingSlash('https://a.b/')).toBe('https://a.b');
    expect(removeTrailingSlash('https://a.b')).toBe('https://a.b');
    expect(removeTrailingSlash('https://a.b//')).toBe('https://a.b/');
  });

  it('maps every falsy input to an empty string', () => {
    expect(removeTrailingSlash('')).toBe('');
    expect(removeTrailingSlash(null)).toBe('');
    expect(removeTrailingSlash(undefined)).toBe('');
  });

  it('reduces a bare slash to an empty string', () => {
    expect(removeTrailingSlash('/')).toBe('');
  });
});

// ── time formatting ─────────────────────────────────────────────────────
describe('timestamp2string', () => {
  it('zero-pads every field to a fixed-width local datetime', () => {
    // Built from a LOCAL Date so the expectation holds in any timezone.
    const ts = Math.floor(new Date(2024, 0, 5, 7, 8, 9).getTime() / 1000);
    expect(timestamp2string(ts)).toBe('2024-01-05 07:08:09');
  });

  it('leaves two-digit fields untouched', () => {
    const ts = Math.floor(new Date(2024, 10, 25, 13, 45, 59).getTime() / 1000);
    expect(timestamp2string(ts)).toBe('2024-11-25 13:45:59');
  });
});

describe('timestamp2string1', () => {
  const ts = Math.floor(new Date(2024, 0, 5, 7, 8, 9).getTime() / 1000);

  it('defaults to hour granularity without the year', () => {
    expect(timestamp2string1(ts)).toBe('01-05 07:00');
  });

  it('prepends the year only when asked (cross-year data)', () => {
    expect(timestamp2string1(ts, 'hour', true)).toBe('2024-01-05 07:00');
  });

  it('renders a 7-day inclusive span in week mode', () => {
    // start + 6 days = 2024-01-11
    expect(timestamp2string1(ts, 'week')).toBe('01-05 - 01-11');
    expect(timestamp2string1(ts, 'week', true)).toBe('2024-01-05 - 2024-01-11');
  });

  it('drops the time entirely for any other granularity', () => {
    expect(timestamp2string1(ts, 'day')).toBe('01-05');
  });
});

describe('isDataCrossYear', () => {
  const jan2024 = Math.floor(new Date(2024, 0, 5).getTime() / 1000);
  const dec2024 = Math.floor(new Date(2024, 11, 30).getTime() / 1000);
  const jan2025 = Math.floor(new Date(2025, 0, 2).getTime() / 1000);

  it('is false for empty or missing input', () => {
    expect(isDataCrossYear([])).toBe(false);
    expect(isDataCrossYear(null)).toBe(false);
    expect(isDataCrossYear(undefined)).toBe(false);
  });

  it('is false when every timestamp lands in one year', () => {
    expect(isDataCrossYear([jan2024, dec2024])).toBe(false);
  });

  it('is true as soon as two years appear', () => {
    expect(isDataCrossYear([dec2024, jan2025])).toBe(true);
  });
});

describe('getTodayStartTimestamp', () => {
  it('returns local midnight of the current day, in seconds', () => {
    const d = new Date(getTodayStartTimestamp() * 1000);
    expect(d.getHours()).toBe(0);
    expect(d.getMinutes()).toBe(0);
    expect(d.getSeconds()).toBe(0);
    expect(d.toDateString()).toBe(new Date().toDateString());
  });
});

describe('formatDateString / formatDateTimeString', () => {
  it('zero-pads month, day, hour and minute', () => {
    const d = new Date(2024, 0, 5, 7, 8);
    expect(formatDateString(d)).toBe('2024-01-05');
    expect(formatDateTimeString(d)).toBe('2024-01-05 07:08');
  });

  it('keeps two-digit fields as-is', () => {
    const d = new Date(2024, 11, 31, 23, 59);
    expect(formatDateString(d)).toBe('2024-12-31');
    expect(formatDateTimeString(d)).toBe('2024-12-31 23:59');
  });
});

describe('getRelativeTime buckets', () => {
  // Fixed local instant so every offset below is exact.
  const NOW = new Date(2026, 5, 15, 12, 0, 0).getTime();
  const DAY = 86400000;

  beforeEach(() => {
    vi.useFakeTimers();
    vi.setSystemTime(NOW);
  });
  afterEach(() => {
    vi.useRealTimers();
  });

  it('returns an empty string for a missing date', () => {
    expect(getRelativeTime('')).toBe('');
    expect(getRelativeTime(null)).toBe('');
    expect(getRelativeTime(undefined)).toBe('');
  });

  it('echoes an unparsable date back unchanged', () => {
    expect(getRelativeTime('not-a-date')).toBe('not-a-date');
  });

  it('picks seconds / minutes / hours / days', () => {
    expect(getRelativeTime(NOW - 30_000)).toBe('刚刚');
    expect(getRelativeTime(NOW - 59_000)).toBe('刚刚');
    expect(getRelativeTime(NOW - 120_000)).toBe('2 分钟前');
    expect(getRelativeTime(NOW - 3 * 3600_000)).toBe('3 小时前');
    expect(getRelativeTime(NOW - 3 * DAY)).toBe('3 天前');
    expect(getRelativeTime(NOW - 6 * DAY)).toBe('6 天前');
  });

  it('picks weeks between 7 and 27 days', () => {
    expect(getRelativeTime(NOW - 7 * DAY)).toBe('1 周前');
    expect(getRelativeTime(NOW - 14 * DAY)).toBe('2 周前');
    expect(getRelativeTime(NOW - 27 * DAY)).toBe('3 周前');
  });

  it('picks months from 30 days on', () => {
    expect(getRelativeTime(NOW - 30 * DAY)).toBe('1 个月前');
    expect(getRelativeTime(NOW - 60 * DAY)).toBe('2 个月前');
  });

  it('falls back to an absolute date for future and very old dates', () => {
    expect(getRelativeTime(NOW + DAY)).toBe('2026-06-16');
    // 800 days before 2026-06-15 is 2024-04-06; diffYears === 2 so the
    // relative buckets stop and the absolute date is shown.
    expect(getRelativeTime(NOW - 800 * DAY)).toBe('2024-04-06');
  });

  it('should not report "0 个月前" for a 28- or 29-day-old item', () => {
    // DEFECT: the week bucket ends at diffWeeks < 4 (i.e. < 28 days) but the
    // month bucket is Math.floor(diffDays / 30), which is still 0 for 28 and
    // 29 days. utils.jsx:579-582 therefore renders the literal string
    // "0 个月前" for announcements aged 28-29 days. Correct contract: keep
    // showing weeks (or round up to 1 month) -- never zero.
    expect(getRelativeTime(NOW - 28 * DAY)).toBe('4 周前');
    expect(getRelativeTime(NOW - 29 * DAY)).toBe('4 周前');
  });
});

// ── JSON validators ─────────────────────────────────────────────────────
describe('verifyJSON / verifyJSONPromise', () => {
  it('accepts any valid JSON document, including scalars', () => {
    expect(verifyJSON('{"a":1}')).toBe(true);
    expect(verifyJSON('[1,2]')).toBe(true);
    expect(verifyJSON('null')).toBe(true);
    expect(verifyJSON('123')).toBe(true);
  });

  it('rejects malformed JSON and bare identifiers', () => {
    expect(verifyJSON('{a:1}')).toBe(false);
    expect(verifyJSON('')).toBe(false);
    expect(verifyJSON('undefined')).toBe(false);
  });

  it('verifyJSONPromise resolves on valid and rejects with a message', async () => {
    await expect(verifyJSONPromise('{"a":1}')).resolves.toBeUndefined();
    await expect(verifyJSONPromise('{a:1}')).rejects.toBe(
      '不是合法的 JSON 字符串',
    );
  });
});

describe('one-shot prompt flags', () => {
  it('shouldShowPrompt is true until setPromptShown records the id', () => {
    expect(shouldShowPrompt('quota-warning')).toBe(true);
    setPromptShown('quota-warning');
    expect(shouldShowPrompt('quota-warning')).toBe(false);
    // ids are independent
    expect(shouldShowPrompt('other')).toBe(true);
  });
});

describe('compareObjects', () => {
  it('reports only keys present in BOTH objects whose value changed', () => {
    expect(compareObjects({ a: 1, b: 2, c: 3 }, { a: 1, b: 99, d: 4 })).toEqual(
      [{ key: 'b', oldValue: 2, newValue: 99 }],
    );
  });

  it('is empty when nothing changed', () => {
    expect(compareObjects({ a: 1 }, { a: 1 })).toEqual([]);
  });

  it('ignores keys added only on the new side', () => {
    expect(compareObjects({}, { a: 1 })).toEqual([]);
  });

  it('ignores keys dropped on the new side', () => {
    expect(compareObjects({ a: 1 }, {})).toEqual([]);
  });

  it('is a shallow reference comparison, so equal nested objects look changed', () => {
    const diff = compareObjects({ a: { x: 1 } }, { a: { x: 1 } });
    expect(diff).toHaveLength(1);
    expect(diff[0].key).toBe('a');
  });
});

// ── playground message helpers ──────────────────────────────────────────
describe('generateMessageId', () => {
  it('yields strictly increasing numeric strings', () => {
    const a = generateMessageId();
    const b = generateMessageId();
    expect(typeof a).toBe('string');
    expect(Number(b)).toBe(Number(a) + 1);
  });
});

describe('getTextContent', () => {
  it('returns a plain string body as-is', () => {
    expect(getTextContent({ content: 'hello' })).toBe('hello');
  });

  it('pulls the first text part out of a multi-modal body', () => {
    expect(
      getTextContent({
        content: [
          { type: 'image_url', image_url: { url: 'u' } },
          { type: 'text', text: 'caption' },
        ],
      }),
    ).toBe('caption');
  });

  it('is an empty string for missing / empty / non-string content', () => {
    expect(getTextContent(null)).toBe('');
    expect(getTextContent({})).toBe('');
    expect(getTextContent({ content: '' })).toBe('');
    expect(getTextContent({ content: [{ type: 'image_url' }] })).toBe('');
    expect(getTextContent({ content: { a: 1 } })).toBe('');
  });
});

describe('processThinkTags', () => {
  it('passes content through untouched when there is no think tag', () => {
    expect(processThinkTags('plain answer')).toEqual({
      content: 'plain answer',
      reasoningContent: '',
    });
  });

  it('lifts a single think block out of the reply', () => {
    expect(processThinkTags('<think>weighing it</think>Answer.')).toEqual({
      content: 'Answer.',
      reasoningContent: 'weighing it',
    });
  });

  it('joins multiple think blocks with a horizontal rule', () => {
    const out = processThinkTags('<think>one</think>A<think>two</think>B');
    expect(out.content).toBe('AB');
    expect(out.reasoningContent).toBe('one\n\n---\n\ntwo');
  });

  it('appends to pre-existing reasoning rather than replacing it', () => {
    const out = processThinkTags('<think>new</think>A', 'earlier');
    expect(out.reasoningContent).toBe('earlier\n\n---\n\nnew');
  });

  it('keeps prior reasoning when the content has no think tag', () => {
    expect(processThinkTags('A', 'earlier')).toEqual({
      content: 'A',
      reasoningContent: 'earlier',
    });
  });

  it('is re-entrant: the shared global regex is reset on every call', () => {
    const first = processThinkTags('<think>x</think>A');
    const second = processThinkTags('<think>x</think>A');
    expect(second).toEqual(first);
  });
});

describe('processIncompleteThinkTags (streaming)', () => {
  it('delegates to processThinkTags when nothing is open', () => {
    expect(processIncompleteThinkTags('<think>done</think>A')).toEqual({
      content: 'A',
      reasoningContent: 'done',
    });
  });

  it('returns empty content for an empty chunk but keeps reasoning', () => {
    expect(processIncompleteThinkTags('', 'so far')).toEqual({
      content: '',
      reasoningContent: 'so far',
    });
  });

  it('moves an unterminated think fragment into reasoning', () => {
    const out = processIncompleteThinkTags('Reply so far<think>still thin');
    expect(out.content).toBe('Reply so far');
    expect(out.reasoningContent).toBe('still thin');
  });

  it('chains an unterminated fragment after earlier reasoning', () => {
    const out = processIncompleteThinkTags('A<think>tail', 'head');
    expect(out.content).toBe('A');
    expect(out.reasoningContent).toBe('head\n\n---\n\ntail');
  });

  it('handles a closed block followed by a freshly opened one', () => {
    const out = processIncompleteThinkTags('<think>first</think>A<think>sec');
    expect(out.content).toBe('A');
    expect(out.reasoningContent).toBe('sec\n\n---\n\nfirst');
  });
});

describe('buildMessageContent', () => {
  it('is an empty string when there is neither text nor image', () => {
    expect(buildMessageContent('', [])).toBe('');
    expect(buildMessageContent('')).toBe('');
  });

  it('returns the bare text when images are disabled', () => {
    expect(buildMessageContent('hi', ['http://x/1.png'], false)).toBe('hi');
  });

  it('returns the bare text when the url list is all blanks', () => {
    expect(buildMessageContent('hi', ['', '   '], true)).toBe('hi');
  });

  it('builds a multi-modal array and trims each url when enabled', () => {
    expect(buildMessageContent('look', [' http://x/1.png '], true)).toEqual([
      { type: 'text', text: 'look' },
      { type: 'image_url', image_url: { url: 'http://x/1.png' } },
    ]);
  });

  it('allows an image-only message (empty text part)', () => {
    expect(buildMessageContent('', ['http://x/1.png'], true)).toEqual([
      { type: 'text', text: '' },
      { type: 'image_url', image_url: { url: 'http://x/1.png' } },
    ]);
  });

  it('should tolerate an explicitly null url list', () => {
    // DEFECT: the `imageUrls = []` default only applies to undefined. With
    // text present and imageUrls === null the early-return guard is skipped
    // and utils.jsx:464 calls null.filter -> TypeError, taking the composer
    // down mid-send. Correct contract: treat null like an empty list.
    expect(buildMessageContent('hi', null, true)).toBe('hi');
  });
});

describe('message construction and inspection', () => {
  it('createMessage stamps role, content, an id and a timestamp', () => {
    const m = createMessage('user', 'hi');
    expect(m.role).toBe('user');
    expect(m.content).toBe('hi');
    expect(typeof m.id).toBe('string');
    expect(typeof m.createAt).toBe('number');
  });

  it('createMessage lets options override the generated fields', () => {
    const m = createMessage('user', 'hi', { id: 'fixed', extra: true });
    expect(m.id).toBe('fixed');
    expect(m.extra).toBe(true);
  });

  it('createLoadingAssistantMessage starts collapsed-open and unfinished', () => {
    const m = createLoadingAssistantMessage();
    expect(m.role).toBe('assistant');
    expect(m.content).toBe('');
    expect(m.status).toBe('loading');
    expect(m.isReasoningExpanded).toBe(true);
    expect(m.isThinkingComplete).toBe(false);
    expect(m.hasAutoCollapsed).toBe(false);
  });

  it('hasImageContent only fires for an array body with an image part', () => {
    expect(hasImageContent({ content: [{ type: 'image_url' }] })).toBe(true);
    expect(hasImageContent({ content: [{ type: 'text' }] })).toBe(false);
    expect(hasImageContent({ content: 'plain' })).toBe(false);
    expect(hasImageContent(null)).toBeFalsy();
  });

  it('formatMessageForAPI strips client-only fields', () => {
    expect(
      formatMessageForAPI({
        role: 'user',
        content: 'hi',
        id: '9',
        createAt: 1,
        status: 'loading',
      }),
    ).toEqual({ role: 'user', content: 'hi' });
    expect(formatMessageForAPI(null)).toBeNull();
  });

  it('isValidMessage accepts an empty string body but not a missing one', () => {
    expect(isValidMessage({ role: 'user', content: '' })).toBeTruthy();
    expect(isValidMessage({ role: 'user', content: 'hi' })).toBeTruthy();
    expect(isValidMessage({ role: 'user' })).toBeFalsy();
    expect(isValidMessage({ content: 'hi' })).toBeFalsy();
    expect(isValidMessage(null)).toBeFalsy();
  });

  it('isValidMessage returns the last truthy operand, not a boolean', () => {
    // Documents a type wart: the `is`-prefixed predicate leaks the message
    // body out of its && chain, so callers doing `=== true` silently break.
    expect(isValidMessage({ role: 'user', content: 'hi' })).toBe('hi');
    // The empty-string case does normalise to a real boolean, so the return
    // type of this one function is genuinely mixed.
    expect(isValidMessage({ role: 'user', content: '' })).toBe(true);
  });
});

describe('last-message lookups scan backwards', () => {
  const history = [
    { role: 'user', content: 'q1' },
    { role: 'assistant', content: 'a1' },
    { role: 'system', content: 's' },
    { role: 'user', content: 'q2' },
    { role: 'assistant', content: 'a2' },
  ];

  it('finds the most recent message of each role', () => {
    expect(getLastUserMessage(history).content).toBe('q2');
    expect(getLastAssistantMessage(history).content).toBe('a2');
  });

  it('returns null when the role is absent or input is not an array', () => {
    expect(getLastUserMessage([{ role: 'system', content: 's' }])).toBeNull();
    expect(getLastAssistantMessage([])).toBeNull();
    expect(getLastUserMessage(null)).toBeNull();
    expect(getLastAssistantMessage('nope')).toBeNull();
  });
});

// ── table density preference ────────────────────────────────────────────
describe('table compact mode', () => {
  it('defaults to false for any table key', () => {
    expect(getTableCompactMode()).toBe(false);
    expect(getTableCompactMode('logs')).toBe(false);
  });

  it('persists per table key without clobbering siblings', () => {
    setTableCompactMode(true, 'logs');
    setTableCompactMode(false, 'channels');
    expect(getTableCompactMode('logs')).toBe(true);
    expect(getTableCompactMode('channels')).toBe(false);
    expect(getTableCompactMode('global')).toBe(false);
  });

  it('coerces stored values to a strict boolean', () => {
    localStorage.setItem(
      'table_compact_modes',
      JSON.stringify({ logs: 'yes' }),
    );
    expect(getTableCompactMode('logs')).toBe(true);
  });

  it('recovers from a corrupted blob instead of throwing', () => {
    localStorage.setItem('table_compact_modes', '{broken');
    expect(getTableCompactMode('logs')).toBe(false);
    setTableCompactMode(true, 'logs');
    expect(getTableCompactMode('logs')).toBe(true);
  });
});

// ── Select filter ───────────────────────────────────────────────────────
describe('selectFilter', () => {
  const option = { value: 'gpt-4o', label: 'GPT 4o (vision)' };

  it('matches everything for an empty query', () => {
    expect(selectFilter('', option)).toBe(true);
    expect(selectFilter(undefined, option)).toBe(true);
  });

  it('matches on value or label, case- and whitespace-insensitively', () => {
    expect(selectFilter('GPT-4', option)).toBe(true);
    expect(selectFilter('  vision  ', option)).toBe(true);
    expect(selectFilter('claude', option)).toBe(false);
  });

  it('tolerates options with a missing value or label', () => {
    expect(selectFilter('x', { label: 'x-ray' })).toBe(true);
    expect(selectFilter('x', { value: 'x-ray' })).toBe(true);
    expect(selectFilter('x', {})).toBe(false);
    expect(selectFilter('x', null)).toBe(false);
  });

  it('stringifies non-string values before matching', () => {
    expect(selectFilter('12', { value: 123, label: null })).toBe(true);
  });
});

// ── model pricing arithmetic ────────────────────────────────────────────
describe('calculateModelPrice', () => {
  // The production caller (useModelPricingData.jsx:174) formats with THREE
  // decimals, so the stub mirrors that exactly.
  const displayPrice = (usd) => `$${usd.toFixed(3)}`;
  const groupRatio = { default: 1, vip: 0.8, svip: 2 };

  it('per-token: price = model_ratio * 2 * group_ratio, per 1M tokens', () => {
    const out = calculateModelPrice({
      record: { quota_type: 0, model_ratio: 0.5, completion_ratio: 3 },
      selectedGroup: 'default',
      groupRatio,
      tokenUnit: 'M',
      displayPrice,
      currency: 'USD',
    });
    // input  0.5 * 2 * 1 = 1.000  -> $1.0000 / M
    // output 0.5 * 3 * 2 * 1 = 3  -> $3.0000 / M
    expect(out).toEqual({
      inputPrice: '$1.0000',
      completionPrice: '$3.0000',
      unitLabel: 'M',
      isPerToken: true,
      usedGroup: 'default',
      usedGroupRatio: 1,
    });
  });

  it('per-token: the K unit divides the per-M figure by 1000', () => {
    const out = calculateModelPrice({
      record: { quota_type: 0, model_ratio: 0.5, completion_ratio: 3 },
      selectedGroup: 'default',
      groupRatio,
      tokenUnit: 'K',
      displayPrice,
      currency: 'USD',
    });
    expect(out.inputPrice).toBe('$0.0010');
    expect(out.completionPrice).toBe('$0.0030');
    expect(out.unitLabel).toBe('K');
  });

  it('per-token: honours an explicit precision', () => {
    const out = calculateModelPrice({
      record: { quota_type: 0, model_ratio: 0.5, completion_ratio: 3 },
      selectedGroup: 'default',
      groupRatio,
      tokenUnit: 'M',
      displayPrice,
      currency: 'USD',
      precision: 2,
    });
    expect(out.inputPrice).toBe('$1.00');
  });

  it('group "all" picks the CHEAPEST enabled group, not the first', () => {
    const out = calculateModelPrice({
      record: {
        quota_type: 0,
        model_ratio: 0.5,
        completion_ratio: 1,
        enable_groups: ['svip', 'default', 'vip'],
      },
      selectedGroup: 'all',
      groupRatio,
      tokenUnit: 'M',
      displayPrice,
      currency: 'USD',
    });
    expect(out.usedGroup).toBe('vip');
    expect(out.usedGroupRatio).toBe(0.8);
    // 0.5 * 2 * 0.8 = 0.800
    expect(out.inputPrice).toBe('$0.8000');
  });

  it('falls back to ratio 1 when the selected group is unknown and no enable_groups', () => {
    const out = calculateModelPrice({
      record: { quota_type: 0, model_ratio: 0.5, completion_ratio: 1 },
      selectedGroup: 'ghost',
      groupRatio,
      tokenUnit: 'M',
      displayPrice,
      currency: 'USD',
    });
    expect(out.usedGroup).toBe('ghost');
    expect(out.usedGroupRatio).toBe(1);
    expect(out.inputPrice).toBe('$1.0000');
  });

  it('ignores enable_groups entries that have no configured ratio', () => {
    const out = calculateModelPrice({
      record: {
        quota_type: 0,
        model_ratio: 1,
        completion_ratio: 1,
        enable_groups: ['unpriced', 'vip'],
      },
      selectedGroup: 'all',
      groupRatio,
      tokenUnit: 'M',
      displayPrice,
      currency: 'USD',
    });
    expect(out.usedGroup).toBe('vip');
    expect(out.usedGroupRatio).toBe(0.8);
  });

  it('per-call (quota_type 1) multiplies model_price by the group ratio', () => {
    const out = calculateModelPrice({
      record: { quota_type: 1, model_price: '0.02' },
      selectedGroup: 'svip',
      groupRatio,
      tokenUnit: 'M',
      displayPrice,
      currency: 'USD',
    });
    // 0.02 * 2 = 0.04
    expect(out).toEqual({
      price: '$0.040',
      isPerToken: false,
      usedGroup: 'svip',
      usedGroupRatio: 2,
    });
  });

  it('returns a dash placeholder for an unknown quota_type', () => {
    const out = calculateModelPrice({
      record: { quota_type: 7 },
      selectedGroup: 'default',
      groupRatio,
      tokenUnit: 'M',
      displayPrice,
      currency: 'USD',
    });
    expect(out.price).toBe('-');
    expect(out.isPerToken).toBe(false);
  });

  it('uses the yuan sign for CNY', () => {
    const out = calculateModelPrice({
      record: { quota_type: 0, model_ratio: 0.5, completion_ratio: 1 },
      selectedGroup: 'default',
      groupRatio,
      tokenUnit: 'M',
      displayPrice: (usd) => `¥${usd.toFixed(3)}`,
      currency: 'CNY',
    });
    expect(out.inputPrice).toBe('¥1.0000');
  });

  it('uses the configured custom symbol for CUSTOM', () => {
    localStorage.setItem(
      'status',
      JSON.stringify({ custom_currency_symbol: '₩' }),
    );
    const out = calculateModelPrice({
      record: { quota_type: 0, model_ratio: 0.5, completion_ratio: 1 },
      selectedGroup: 'default',
      groupRatio,
      tokenUnit: 'M',
      displayPrice,
      currency: 'CUSTOM',
    });
    expect(out.inputPrice).toBe('₩1.0000');
  });

  it('falls back to a generic sign when CUSTOM has no usable status', () => {
    const out = calculateModelPrice({
      record: { quota_type: 0, model_ratio: 0.5, completion_ratio: 1 },
      selectedGroup: 'default',
      groupRatio,
      tokenUnit: 'M',
      displayPrice,
      currency: 'CUSTOM',
    });
    expect(out.inputPrice).toBe('¤1.0000');
  });

  it('should not lose digits to the display-string round trip', () => {
    // DEFECT: utils.jsx:702-705 re-parses the *formatted* string
    // (parseFloat(rawDisplayInput.replace(/[^0-9.]/g,''))) instead of using
    // the number it already has. The production displayPrice formats to 3
    // decimals, so anything below $0.0005 per 1M tokens is quantised to 0
    // BEFORE `precision: 4` is applied. A model priced at $0.0004/1M renders
    // as $0.0000 -- reads as free.
    const out = calculateModelPrice({
      record: { quota_type: 0, model_ratio: 0.0002, completion_ratio: 1 },
      selectedGroup: 'default',
      groupRatio,
      tokenUnit: 'M',
      displayPrice,
      currency: 'USD',
    });
    expect(out.inputPrice).toBe('$0.0004');
  });

  it('should not print NaN when completion_ratio is missing', () => {
    // DEFECT: a record without completion_ratio makes completionRatioPriceUSD
    // NaN; displayPrice yields '$NaN'; the [^0-9.] strip leaves an empty
    // string; parseFloat('') is NaN again, so the pricing table shows the
    // literal '$NaN'. Correct contract: treat a missing completion ratio as
    // the input ratio (or render the dash placeholder).
    const out = calculateModelPrice({
      record: { quota_type: 0, model_ratio: 0.5 },
      selectedGroup: 'default',
      groupRatio,
      tokenUnit: 'M',
      displayPrice,
      currency: 'USD',
    });
    expect(out.completionPrice).not.toContain('NaN');
    // A missing completion ratio bills output at the input ratio:
    // 0.5 * 1 * 2 * 1 = $1.0000 per 1M tokens.
    expect(out.inputPrice).toBe('$1.0000');
    expect(out.completionPrice).toBe('$1.0000');
  });
});

describe('resetPricingFilters', () => {
  it('pushes the documented defaults into every setter', () => {
    const spies = {
      handleChange: vi.fn(),
      setShowWithRecharge: vi.fn(),
      setCurrency: vi.fn(),
      setShowRatio: vi.fn(),
      setViewMode: vi.fn(),
      setFilterGroup: vi.fn(),
      setFilterQuotaType: vi.fn(),
      setFilterEndpointType: vi.fn(),
      setFilterVendor: vi.fn(),
      setFilterTag: vi.fn(),
      setCurrentPage: vi.fn(),
      setTokenUnit: vi.fn(),
    };
    resetPricingFilters(spies);

    expect(spies.handleChange).toHaveBeenCalledWith('');
    expect(spies.setShowWithRecharge).toHaveBeenCalledWith(false);
    expect(spies.setCurrency).toHaveBeenCalledWith('USD');
    expect(spies.setShowRatio).toHaveBeenCalledWith(false);
    expect(spies.setViewMode).toHaveBeenCalledWith('card');
    expect(spies.setTokenUnit).toHaveBeenCalledWith('M');
    expect(spies.setFilterGroup).toHaveBeenCalledWith('all');
    expect(spies.setFilterQuotaType).toHaveBeenCalledWith('all');
    expect(spies.setFilterEndpointType).toHaveBeenCalledWith('all');
    expect(spies.setFilterVendor).toHaveBeenCalledWith('all');
    expect(spies.setFilterTag).toHaveBeenCalledWith('all');
    expect(spies.setCurrentPage).toHaveBeenCalledWith(1);
  });

  it('is a no-op (not a crash) when a page passes only some setters', () => {
    const setCurrency = vi.fn();
    expect(() => resetPricingFilters({ setCurrency })).not.toThrow();
    expect(setCurrency).toHaveBeenCalledWith('USD');
  });
});
