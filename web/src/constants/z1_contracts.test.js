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
import { describe, it, expect, vi } from 'vitest';

import {
  CHANNEL_OPTIONS,
  CHANNEL_PRESETS,
  MODEL_TABLE_PAGE_SIZE,
} from './channel.constants';
import {
  ITEMS_PER_PAGE,
  DEFAULT_ENDPOINT,
  TABLE_COMPACT_MODES_KEY,
  API_ENDPOINTS as RELAY_ENDPOINTS,
  TASK_ACTION_GENERATE,
  TASK_ACTION_TEXT_GENERATE,
  TASK_ACTION_FIRST_TAIL_GENERATE,
  TASK_ACTION_REFERENCE_GENERATE,
  TASK_ACTION_REMIX_GENERATE,
} from './common.constant';
import {
  TIME_OPTIONS,
  DEFAULT_TIME_INTERVALS,
  DEFAULT_TIME_RANGE,
  UPTIME_STATUS_MAP,
  ANNOUNCEMENT_LEGEND_DATA,
  STORAGE_KEYS as DASHBOARD_STORAGE_KEYS,
  DEFAULTS,
  DEFAULT_CHART_SPECS,
  ILLUSTRATION_SIZE,
} from './dashboard.constants';
import {
  MESSAGE_ROLES,
  MESSAGE_STATUS,
  DEBUG_TABS,
  API_ENDPOINTS as PLAYGROUND_ENDPOINTS,
  DEFAULT_CONFIG,
  DEFAULT_MESSAGES,
  getDefaultMessages,
  THINK_TAG_REGEX,
  ERROR_MESSAGES,
  STORAGE_KEYS as PLAYGROUND_STORAGE_KEYS,
} from './playground.constants';
import {
  REDEMPTION_STATUS,
  REDEMPTION_STATUS_MAP,
  REDEMPTION_ACTIONS,
} from './redemption.constants';
import { toastConstants } from './toast.constants';
import { userConstants } from './user.constants';
import { DATE_RANGE_PRESETS } from './console.constants';
import * as barrel from './index';

const dupes = (values) => values.filter((v, i) => values.indexOf(v) !== i);

describe('channel.constants — CHANNEL_OPTIONS', () => {
  it('every option is a well-formed {value, color, label} triple', () => {
    for (const opt of CHANNEL_OPTIONS) {
      expect(Number.isInteger(opt.value)).toBe(true);
      expect(opt.value).toBeGreaterThan(0);
      expect(typeof opt.label).toBe('string');
      expect(opt.label.trim()).not.toBe('');
      expect(typeof opt.color).toBe('string');
      expect(opt.color.trim()).not.toBe('');
    }
  });

  // The channel-type value is the wire code sent to the backend. A duplicate
  // makes two vendors indistinguishable and silently routes to the wrong
  // adapter; the Semi Select would also collapse the pair into one row.
  it('has no duplicate channel type codes', () => {
    expect(dupes(CHANNEL_OPTIONS.map((o) => o.value))).toEqual([]);
  });

  it('has no duplicate labels', () => {
    expect(dupes(CHANNEL_OPTIONS.map((o) => o.label))).toEqual([]);
  });
});

describe('channel.constants — CHANNEL_PRESETS', () => {
  it('every preset targets a channel type that actually exists', () => {
    const known = new Set(CHANNEL_OPTIONS.map((o) => o.value));
    for (const key of Object.keys(CHANNEL_PRESETS)) {
      expect(known.has(Number(key))).toBe(true);
    }
  });

  it('every preset carries a name and a preset_tip_* i18n key', () => {
    for (const [key, preset] of Object.entries(CHANNEL_PRESETS)) {
      expect(preset.name, key).toBeTypeOf('string');
      expect(preset.name.trim(), key).not.toBe('');
      expect(preset.tip, key).toMatch(/^preset_tip_[a-z0-9]+$/);
    }
  });

  it('every non-empty base_url is an absolute http(s) origin', () => {
    for (const [key, preset] of Object.entries(CHANNEL_PRESETS)) {
      expect(typeof preset.base_url, key).toBe('string');
      if (preset.base_url === '') continue;
      const url = new URL(preset.base_url);
      expect(['http:', 'https:'], key).toContain(url.protocol);
      expect(url.hostname, key).not.toBe('');
    }
  });

  // base_url is concatenated with request paths that already start with '/'.
  // A trailing slash produces '//v1/chat/completions' upstream.
  it('no base_url ends in a slash', () => {
    for (const [key, preset] of Object.entries(CHANNEL_PRESETS)) {
      expect(preset.base_url.endsWith('/'), key).toBe(false);
    }
  });

  it('preset tips are unique per vendor', () => {
    const tips = Object.values(CHANNEL_PRESETS).map((p) => p.tip);
    expect(dupes(tips)).toEqual([]);
  });
});

describe('page-size constants', () => {
  // Comment on the source line: "this value must keep same as the one
  // defined in backend!". Backend truth is
  // internal/pkg/common/constants.go -> `var ItemsPerPage = 10`.
  it('ITEMS_PER_PAGE mirrors the backend page size of 10', () => {
    expect(ITEMS_PER_PAGE).toBe(10);
  });

  // The model-table page size is declared twice, in two different files.
  // They must not drift apart.
  it('MODEL_TABLE_PAGE_SIZE agrees with DEFAULTS.MODEL_TABLE_PAGE_SIZE', () => {
    expect(MODEL_TABLE_PAGE_SIZE).toBe(DEFAULTS.MODEL_TABLE_PAGE_SIZE);
    expect(Number.isInteger(MODEL_TABLE_PAGE_SIZE)).toBe(true);
    expect(MODEL_TABLE_PAGE_SIZE).toBeGreaterThan(0);
  });

  it('every DEFAULTS entry is a positive integer', () => {
    for (const [key, value] of Object.entries(DEFAULTS)) {
      expect(Number.isInteger(value), key).toBe(true);
      expect(value, key).toBeGreaterThan(0);
    }
  });
});

describe('common.constant — relay endpoint catalogue', () => {
  it('lists absolute, versioned, query-free paths', () => {
    for (const endpoint of RELAY_ENDPOINTS) {
      expect(endpoint).toMatch(/^\/v1(beta)?\//);
      expect(endpoint).not.toContain('?');
      expect(endpoint.endsWith('/')).toBe(false);
    }
  });

  it('has no duplicate endpoints', () => {
    expect(dupes([...RELAY_ENDPOINTS])).toEqual([]);
  });

  it('covers the chat, embeddings, images and audio surfaces', () => {
    expect(RELAY_ENDPOINTS).toContain('/v1/chat/completions');
    expect(RELAY_ENDPOINTS).toContain('/v1/embeddings');
    expect(RELAY_ENDPOINTS).toContain('/v1/images/generations');
    expect(RELAY_ENDPOINTS).toContain('/v1/audio/transcriptions');
  });

  it('DEFAULT_ENDPOINT and TABLE_COMPACT_MODES_KEY hold their wire values', () => {
    expect(DEFAULT_ENDPOINT).toBe('/api/ratio_config');
    expect(TABLE_COMPACT_MODES_KEY).toBe('table_compact_modes');
  });

  // Task actions are compared by string identity when dispatching a task
  // submit; a copy-paste collision would route two actions to one handler.
  it('task action names are distinct', () => {
    const actions = [
      TASK_ACTION_GENERATE,
      TASK_ACTION_TEXT_GENERATE,
      TASK_ACTION_FIRST_TAIL_GENERATE,
      TASK_ACTION_REFERENCE_GENERATE,
      TASK_ACTION_REMIX_GENERATE,
    ];
    expect(dupes(actions)).toEqual([]);
    expect(actions.every((a) => typeof a === 'string' && a !== '')).toBe(true);
  });
});

describe('dashboard.constants — time buckets', () => {
  // Pure arithmetic contract: the two units of the same bucket must agree,
  // otherwise a chart drawn from `seconds` and a range picked from
  // `minutes` disagree about how wide one bucket is.
  it('seconds and minutes describe the same bucket width', () => {
    for (const [key, spec] of Object.entries(DEFAULT_TIME_INTERVALS)) {
      expect(spec.seconds, key).toBe(spec.minutes * 60);
    }
  });

  it('bucket widths match their real-world durations', () => {
    expect(DEFAULT_TIME_INTERVALS.hour.seconds).toBe(3600);
    expect(DEFAULT_TIME_INTERVALS.day.seconds).toBe(24 * 3600);
    expect(DEFAULT_TIME_INTERVALS.week.seconds).toBe(7 * 24 * 3600);
  });

  it('the selectable options, the interval table and the range enum agree', () => {
    const optionValues = TIME_OPTIONS.map((o) => o.value).sort();
    expect(optionValues).toEqual(Object.keys(DEFAULT_TIME_INTERVALS).sort());
    expect(optionValues).toEqual(Object.values(DEFAULT_TIME_RANGE).sort());
  });

  it('every time option carries a non-empty label', () => {
    for (const option of TIME_OPTIONS) {
      expect(option.label.trim()).not.toBe('');
    }
  });
});

describe('dashboard.constants — chart specs', () => {
  // These exports were reached by `import` alone, which counted them as covered
  // while nothing checked them. Only the specs carry a real invariant; the rest
  // of that file's untested exports (CHART_CONFIG, CARD_PROPS, FORM_FIELD_PROPS,
  // ICON_BUTTON_CLASS, FLEX_CENTER_GAP2) are bare style literals whose only
  // possible assertion is "equals itself" — a change-detector that fails on
  // every restyle and catches nothing. They are left deliberately unasserted;
  // recorded here so the gap is disclosed rather than hidden.
  it('each spec declares the chart type its key promises', () => {
    expect(DEFAULT_CHART_SPECS.PIE.type).toBe('pie');
    expect(DEFAULT_CHART_SPECS.BAR.type).toBe('bar');
    expect(DEFAULT_CHART_SPECS.LINE.type).toBe('line');
  });

  it('the donut radii are ordered and inside the unit circle', () => {
    const pie = DEFAULT_CHART_SPECS.PIE;
    const hovered = pie.pie.state.hover.outerRadius;

    // inner < outer < hover-outer <= 1. Out of order renders a filled disc or
    // an inverted ring, and > 1 clips the chart against its own viewport.
    expect(pie.innerRadius).toBeGreaterThan(0);
    expect(pie.innerRadius).toBeLessThan(pie.outerRadius);
    expect(pie.outerRadius).toBeLessThan(hovered);
    expect(hovered).toBeLessThanOrEqual(1);
  });

  it('the stacked bar keeps single-select legends', () => {
    // A stacked bar with multi-select legends lets the user hide a middle
    // series, leaving the remaining stack silently mis-totalled.
    expect(DEFAULT_CHART_SPECS.BAR.stack).toBe(true);
    expect(DEFAULT_CHART_SPECS.BAR.legends.selectMode).toBe('single');
  });

  it('the illustration box is a positive square', () => {
    expect(ILLUSTRATION_SIZE.width).toBeGreaterThan(0);
    expect(ILLUSTRATION_SIZE.height).toBe(ILLUSTRATION_SIZE.width);
  });
});

describe('dashboard.constants — status legends', () => {
  it('UPTIME_STATUS_MAP covers every uptime code with a hex colour', () => {
    for (const code of ['0', '1', '2', '3']) {
      const entry = UPTIME_STATUS_MAP[code];
      expect(entry, code).toBeDefined();
      expect(entry.color, code).toMatch(/^#[0-9a-f]{6}$/i);
      expect(entry.label.trim(), code).not.toBe('');
      expect(entry.text.trim(), code).not.toBe('');
    }
  });

  // UP and DOWN are the only two codes an operator reads at a glance; if
  // they ever share a colour the whole status strip becomes meaningless.
  it('UP and DOWN are rendered in different colours', () => {
    expect(UPTIME_STATUS_MAP[1].color).not.toBe(UPTIME_STATUS_MAP[0].color);
  });

  it('announcement legend types are unique', () => {
    expect(dupes(ANNOUNCEMENT_LEGEND_DATA.map((a) => a.type))).toEqual([]);
    expect(dupes(ANNOUNCEMENT_LEGEND_DATA.map((a) => a.color))).toEqual([]);
  });

  it('dashboard storage keys are unique, non-empty snake_case strings', () => {
    const values = Object.values(DASHBOARD_STORAGE_KEYS);
    expect(dupes(values)).toEqual([]);
    for (const value of values) {
      expect(value).toMatch(/^[a-z][a-z0-9_]*$/);
    }
  });
});

describe('playground.constants', () => {
  it('message roles use the vendor-neutral chat role names', () => {
    expect(MESSAGE_ROLES).toEqual({
      USER: 'user',
      ASSISTANT: 'assistant',
      SYSTEM: 'system',
    });
  });

  it('message statuses are distinct', () => {
    expect(dupes(Object.values(MESSAGE_STATUS))).toEqual([]);
  });

  it('debug tab ids are distinct', () => {
    expect(dupes(Object.values(DEBUG_TABS))).toEqual([]);
  });

  // Backend truth: internal/adapter/handler/router/relay-router.go mounts
  // the playground group at "/pg".
  it('the playground chat endpoint targets the /pg relay group', () => {
    expect(PLAYGROUND_ENDPOINTS.CHAT_COMPLETIONS).toBe('/pg/chat/completions');
    expect(PLAYGROUND_ENDPOINTS.USER_MODELS).toBe('/api/user/models');
    expect(PLAYGROUND_ENDPOINTS.USER_GROUPS).toBe('/api/user/self/groups');
  });

  it('the default sampling parameters sit inside their legal ranges', () => {
    const {
      temperature,
      top_p,
      max_tokens,
      frequency_penalty,
      presence_penalty,
    } = DEFAULT_CONFIG.inputs;
    expect(temperature).toBeGreaterThanOrEqual(0);
    expect(temperature).toBeLessThanOrEqual(2);
    expect(top_p).toBeGreaterThan(0);
    expect(top_p).toBeLessThanOrEqual(1);
    expect(Number.isInteger(max_tokens)).toBe(true);
    expect(max_tokens).toBeGreaterThan(0);
    for (const penalty of [frequency_penalty, presence_penalty]) {
      expect(penalty).toBeGreaterThanOrEqual(-2);
      expect(penalty).toBeLessThanOrEqual(2);
    }
  });

  it('every toggle in parameterEnabled names a real input parameter', () => {
    for (const key of Object.keys(DEFAULT_CONFIG.parameterEnabled)) {
      expect(
        Object.prototype.hasOwnProperty.call(DEFAULT_CONFIG.inputs, key),
        key,
      ).toBe(true);
    }
  });

  it('image support is off by default and seeded with one empty slot', () => {
    expect(DEFAULT_CONFIG.inputs.imageEnabled).toBe(false);
    expect(DEFAULT_CONFIG.inputs.imageUrls).toEqual(['']);
  });

  it('getDefaultMessages runs every user-visible string through t()', () => {
    const t = vi.fn((key) => `T:${key}`);
    const messages = getDefaultMessages(t);

    expect(t).toHaveBeenCalledTimes(2);
    expect(messages).toHaveLength(2);
    expect(messages[0].role).toBe(MESSAGE_ROLES.USER);
    expect(messages[1].role).toBe(MESSAGE_ROLES.ASSISTANT);
    expect(messages[0].content).toBe('T:默认用户消息');
    expect(messages[1].content).toBe('T:默认助手消息');
    expect(messages[0].id).not.toBe(messages[1].id);
  });

  it('getDefaultMessages returns a fresh array each call (no shared state)', () => {
    const t = (key) => key;
    const a = getDefaultMessages(t);
    const b = getDefaultMessages(t);
    expect(a).not.toBe(b);
    expect(a[0]).not.toBe(b[0]);
  });

  it('the legacy DEFAULT_MESSAGES keeps the same role ordering', () => {
    expect(DEFAULT_MESSAGES.map((m) => m.role)).toEqual([
      MESSAGE_ROLES.USER,
      MESSAGE_ROLES.ASSISTANT,
    ]);
    expect(DEFAULT_MESSAGES[1].isReasoningExpanded).toBe(false);
  });

  it('THINK_TAG_REGEX captures multi-line reasoning blocks', () => {
    THINK_TAG_REGEX.lastIndex = 0;
    const content = 'a<think>line1\nline2</think>b<think>second</think>c';
    const found = [...content.matchAll(THINK_TAG_REGEX)].map((m) => m[1]);
    expect(found).toEqual(['line1\nline2', 'second']);
  });

  it('THINK_TAG_REGEX does not match an unclosed think tag', () => {
    THINK_TAG_REGEX.lastIndex = 0;
    expect(THINK_TAG_REGEX.test('<think>never closed')).toBe(false);
    THINK_TAG_REGEX.lastIndex = 0;
  });

  // The regex is a module-level /g singleton, so `lastIndex` survives
  // between callers. helpers/utils.jsx resets it before each scan; this
  // test pins WHY that reset is mandatory.
  it('THINK_TAG_REGEX carries lastIndex across calls unless reset', () => {
    THINK_TAG_REGEX.lastIndex = 0;
    const input = '<think>x</think>';
    expect(THINK_TAG_REGEX.test(input)).toBe(true);
    expect(THINK_TAG_REGEX.lastIndex).toBeGreaterThan(0);
    expect(THINK_TAG_REGEX.test(input)).toBe(false);
    THINK_TAG_REGEX.lastIndex = 0;
    expect(THINK_TAG_REGEX.test(input)).toBe(true);
    THINK_TAG_REGEX.lastIndex = 0;
  });

  it('error messages are unique non-empty strings', () => {
    const values = Object.values(ERROR_MESSAGES);
    expect(dupes(values)).toEqual([]);
    for (const value of values) expect(value.trim()).not.toBe('');
  });

  it('playground storage keys are namespaced to the playground', () => {
    for (const value of Object.values(PLAYGROUND_STORAGE_KEYS)) {
      expect(value).toMatch(/^playground_/);
    }
    expect(dupes(Object.values(PLAYGROUND_STORAGE_KEYS))).toEqual([]);
  });
});

describe('redemption.constants', () => {
  // Backend truth: internal/pkg/common/constants.go
  //   RedemptionCodeStatusEnabled = 1 / Disabled = 2 / Used = 3
  // ("don't use 0, 0 is the default value").
  it('status codes match the backend redemption codes', () => {
    expect(REDEMPTION_STATUS).toEqual({ UNUSED: 1, DISABLED: 2, USED: 3 });
    expect(Object.values(REDEMPTION_STATUS)).not.toContain(0);
  });

  it('every status has exactly one display mapping and no orphans exist', () => {
    const codes = Object.values(REDEMPTION_STATUS).map(String);
    expect(Object.keys(REDEMPTION_STATUS_MAP).sort()).toEqual(codes.sort());
    for (const code of codes) {
      expect(REDEMPTION_STATUS_MAP[code].color.trim(), code).not.toBe('');
      expect(REDEMPTION_STATUS_MAP[code].text.trim(), code).not.toBe('');
    }
  });

  it('a used code is not painted like an unused one', () => {
    expect(REDEMPTION_STATUS_MAP[REDEMPTION_STATUS.USED].color).not.toBe(
      REDEMPTION_STATUS_MAP[REDEMPTION_STATUS.UNUSED].color,
    );
  });

  it('bulk actions are distinct lower-case verbs', () => {
    const values = Object.values(REDEMPTION_ACTIONS);
    expect(dupes(values)).toEqual([]);
    for (const value of values) expect(value).toMatch(/^[a-z]+$/);
  });
});

describe('toast.constants', () => {
  it('every timeout is a positive whole number of milliseconds', () => {
    for (const [key, value] of Object.entries(toastConstants)) {
      expect(Number.isInteger(value), key).toBe(true);
      expect(value, key).toBeGreaterThan(0);
    }
  });

  // A success toast that outlives an error toast would bury the failure.
  it('errors stay on screen longer than successes and infos', () => {
    expect(toastConstants.ERROR_TIMEOUT).toBeGreaterThan(
      toastConstants.SUCCESS_TIMEOUT,
    );
    expect(toastConstants.ERROR_TIMEOUT).toBeGreaterThan(
      toastConstants.INFO_TIMEOUT,
    );
    expect(toastConstants.NOTICE_TIMEOUT).toBe(
      Math.max(...Object.values(toastConstants)),
    );
  });
});

describe('user.constants', () => {
  it('every action name is unique', () => {
    expect(dupes(Object.values(userConstants))).toEqual([]);
  });

  // Naming drift check: each value is mechanically 'USERS_' + its key. A
  // hand-edited value that stops matching is the classic copy-paste bug in
  // this kind of table.
  it('every value is USERS_ + its own key', () => {
    for (const [key, value] of Object.entries(userConstants)) {
      expect(value, key).toBe(`USERS_${key}`);
    }
  });
});

describe('console.constants — DATE_RANGE_PRESETS', () => {
  const withClock = (iso, fn) => {
    vi.useFakeTimers();
    vi.setSystemTime(new Date(iso));
    try {
      fn();
    } finally {
      vi.useRealTimers();
    }
  };

  const DAY_MS = 24 * 60 * 60 * 1000;
  const byText = (text) => DATE_RANGE_PRESETS.find((p) => p.text === text);

  it('every preset exposes a label and lazy start/end factories', () => {
    for (const preset of DATE_RANGE_PRESETS) {
      expect(preset.text.trim()).not.toBe('');
      expect(preset.start).toBeTypeOf('function');
      expect(preset.end).toBeTypeOf('function');
    }
    expect(dupes(DATE_RANGE_PRESETS.map((p) => p.text))).toEqual([]);
  });

  it('every preset produces a start no later than its end', () => {
    withClock('2026-08-17T13:45:30.000', () => {
      for (const preset of DATE_RANGE_PRESETS) {
        expect(preset.start().getTime(), preset.text).toBeLessThanOrEqual(
          preset.end().getTime(),
        );
      }
    });
  });

  it('"今天" spans exactly the current calendar day', () => {
    withClock('2026-08-17T13:45:30.000', () => {
      const preset = byText('今天');
      const start = preset.start();
      const end = preset.end();
      expect(start.getDate()).toBe(17);
      expect(start.getHours()).toBe(0);
      expect(start.getMinutes()).toBe(0);
      expect(start.getSeconds()).toBe(0);
      expect(start.getMilliseconds()).toBe(0);
      expect(end.getDate()).toBe(17);
      expect(end.getHours()).toBe(23);
      expect(end.getMinutes()).toBe(59);
      expect(end.getSeconds()).toBe(59);
    });
  });

  it('"近 7 天" is inclusive of today and exactly 7 calendar days wide', () => {
    withClock('2026-08-17T13:45:30.000', () => {
      const preset = byText('近 7 天');
      const start = preset.start();
      const end = preset.end();
      expect(start.getDate()).toBe(11);
      expect(end.getDate()).toBe(17);
      expect(Math.round((end - start) / DAY_MS)).toBe(7);
    });
  });

  it('"近 30 天" is inclusive of today and exactly 30 calendar days wide', () => {
    withClock('2026-08-17T13:45:30.000', () => {
      const preset = byText('近 30 天');
      const start = preset.start();
      const end = preset.end();
      expect(start.getMonth()).toBe(6); // July
      expect(start.getDate()).toBe(19);
      expect(end.getDate()).toBe(17);
      expect(Math.round((end - start) / DAY_MS)).toBe(30);
    });
  });

  it('"近 7 天" crosses a month boundary correctly', () => {
    withClock('2026-03-02T09:00:00.000', () => {
      const start = byText('近 7 天').start();
      expect(start.getMonth()).toBe(1); // February
      expect(start.getDate()).toBe(24);
    });
  });

  it('"本月" spans the first to the last day of the current month', () => {
    withClock('2026-02-14T10:00:00.000', () => {
      const preset = byText('本月');
      const start = preset.start();
      const end = preset.end();
      expect(start.getDate()).toBe(1);
      expect(start.getHours()).toBe(0);
      expect(end.getMonth()).toBe(1);
      expect(end.getDate()).toBe(28); // 2026 is not a leap year
      expect(end.getHours()).toBe(23);
    });
  });

  // The presets are factories, not precomputed Dates. A long-lived console
  // tab must pick up the new day without a reload.
  it('re-evaluates against the clock at call time', () => {
    const preset = byText('今天');
    let first;
    let second;
    withClock('2026-08-17T13:45:30.000', () => {
      first = preset.start();
    });
    withClock('2026-08-18T00:05:00.000', () => {
      second = preset.start();
    });
    expect(first.getDate()).toBe(17);
    expect(second.getDate()).toBe(18);
  });

  it('"本周" is exactly seven days wide', () => {
    withClock('2026-08-17T13:45:30.000', () => {
      const preset = byText('本周');
      expect(Math.round((preset.end() - preset.start()) / DAY_MS)).toBe(7);
    });
  });

  // DEFECT (see report): no dayjs locale / weekStart is configured anywhere
  // in the app, so startOf('week') uses the built-in English default and
  // the week runs Sunday..Saturday. The preset label is the zh key "本周"
  // and the Semi date picker renders a Monday-first grid for zh-CN, so the
  // highlighted range disagrees with the calendar the user is looking at.
  // Unskip once dayjs is given a locale (or the weekday plugin with an
  // explicit weekStart).
  it.skip('"本周" starts on Monday', () => {
    withClock('2026-08-17T13:45:30.000', () => {
      // 2026-08-17 is a Monday; the week start must be that same day.
      expect(byText('本周').start().getDay()).toBe(1);
    });
  });

  it('"本周" currently starts on Sunday', () => {
    withClock('2026-08-17T13:45:30.000', () => {
      const start = byText('本周').start();
      expect(start.getDay()).toBe(0);
      expect(start.getDate()).toBe(16);
    });
  });
});

describe('constants barrel (index.js)', () => {
  it('re-exports the channel and redemption tables unchanged', () => {
    expect(barrel.CHANNEL_OPTIONS).toBe(CHANNEL_OPTIONS);
    expect(barrel.REDEMPTION_STATUS).toBe(REDEMPTION_STATUS);
    expect(barrel.toastConstants).toBe(toastConstants);
    expect(barrel.userConstants).toBe(userConstants);
  });

  // DEFECT (see report): common.constant and playground.constants both
  // export API_ENDPOINTS, and dashboard.constants and playground.constants
  // both export STORAGE_KEYS. `export *` cannot disambiguate them, so the
  // barrel silently drops one of each pair (and the winner differs between
  // the dev/test module runner and a Rollup production build, where an
  // ambiguous star re-export is elided entirely).
  // Unskip once the colliding names are renamed or re-exported explicitly.
  it.skip('exposes no ambiguous names across its star re-exports', async () => {
    const modules = await Promise.all([
      import('./channel.constants'),
      import('./user.constants'),
      import('./toast.constants'),
      import('./common.constant'),
      import('./dashboard.constants'),
      import('./playground.constants'),
      import('./redemption.constants'),
    ]);
    const names = modules.flatMap((m) =>
      Object.keys(m).filter((k) => k !== 'default'),
    );
    expect(dupes(names)).toEqual([]);
  });

  // Blast radius of the collision, pinned: anyone importing API_ENDPOINTS
  // or STORAGE_KEYS from '@/constants' expecting the playground versions
  // gets a silently different object and reads undefined off it.
  it('currently resolves the colliding names to the non-playground module', () => {
    expect(barrel.API_ENDPOINTS).not.toBe(PLAYGROUND_ENDPOINTS);
    expect(barrel.API_ENDPOINTS.CHAT_COMPLETIONS).toBeUndefined();
    expect(barrel.STORAGE_KEYS).not.toBe(PLAYGROUND_STORAGE_KEYS);
    expect(barrel.STORAGE_KEYS.CONFIG).toBeUndefined();
  });

  // console.constants is deliberately absent from the barrel, so the date
  // presets can only be reached by their direct path.
  it('does not surface DATE_RANGE_PRESETS', () => {
    expect(barrel.DATE_RANGE_PRESETS).toBeUndefined();
    expect(DATE_RANGE_PRESETS).toHaveLength(5);
  });
});
