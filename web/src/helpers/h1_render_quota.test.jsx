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
import { describe, it, expect, beforeEach, vi } from 'vitest';

// utils.jsx (imported by render.jsx) calls window.matchMedia at MODULE scope
// with no guard, so the import itself throws under jsdom. Install a stub
// before any import runs. See the report for the underlying defect.
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

// The Semi UI barrel drags in lottie-web, which needs a real canvas that
// jsdom does not provide. Stand in a minimal set of the four components
// render.jsx actually uses; every assertion in this file is about the money
// arithmetic, which never touches them.
vi.mock('@douyinfe/semi-ui', () => {
  const Tag = ({ children, ...rest }) =>
    React.createElement('span', { 'data-tag': true, ...rest }, children);
  const Text = ({ children, ...rest }) =>
    React.createElement('span', rest, children);
  return {
    Tag,
    Typography: { Text },
    Avatar: ({ children, ...rest }) =>
      React.createElement('span', rest, children),
    Modal: { error: vi.fn() },
  };
});

// render.jsx pulls i18next in directly (not through src/i18n), so in a unit
// test it would be uninitialised and t() would return undefined. Replace it
// with a deterministic echo+interpolate stub so the assertions below are
// about the money arithmetic, not about i18n bootstrap timing.
vi.mock('i18next', () => {
  const stub = {
    language: 'zh',
    t: (key, vars) =>
      vars
        ? String(key).replace(/\{\{(\w+)\}\}/g, (_, name) =>
            vars[name] === undefined ? `{{${name}}}` : String(vars[name]),
          )
        : key,
    // src/i18n/i18n.js (reached transitively via utils -> errorMessages)
    // chains .use().use().init() on this same default export.
    use() {
      return stub;
    },
    init: () => Promise.resolve(),
    changeLanguage: () => Promise.resolve(),
    on: () => {},
    off: () => {},
  };
  return { default: stub };
});

import {
  renderNumber,
  renderNumberWithPoint,
  renderQuotaNumberWithDigit,
  getQuotaPerUnit,
  renderUnitWithQuota,
  getQuotaWithUnit,
  renderQuotaWithAmount,
  getCurrencyConfig,
  convertUSDToCurrency,
  renderQuota,
  renderQuotaWithPrompt,
  renderText,
  stringToColor,
  modelToColor,
  modelColorMap,
} from './render';

// The platform's canonical unit rate: 500_000 internal quota units == $1.
// Every case below is hand-computed from that constant, never copied out of
// a previous run.
const UNIT = '500000';

beforeEach(() => {
  localStorage.clear();
});

describe('renderNumber magnitude buckets', () => {
  it('leaves values below 10k untouched', () => {
    expect(renderNumber(0)).toBe(0);
    expect(renderNumber(999)).toBe(999);
    expect(renderNumber(9999)).toBe(9999);
  });

  it('switches to k at 10_000 and to M at 1_000_000', () => {
    // 10000 / 1000 = 10 -> '10.0k'
    expect(renderNumber(10000)).toBe('10.0k');
    // 12345 / 1000 = 12.345 -> '12.3k'
    expect(renderNumber(12345)).toBe('12.3k');
    // 1_000_000 / 1_000_000 = 1 -> '1.0M'
    expect(renderNumber(1000000)).toBe('1.0M');
    // 2_500_000 / 1_000_000 = 2.5 -> '2.5M'
    expect(renderNumber(2500000)).toBe('2.5M');
  });

  it('switches to B at 1_000_000_000', () => {
    expect(renderNumber(1000000000)).toBe('1.0B');
    expect(renderNumber(1500000000)).toBe('1.5B');
  });

  it('never abbreviates negatives (they fall through every threshold)', () => {
    expect(renderNumber(-25000)).toBe(-25000);
    expect(renderNumber(-1500000)).toBe(-1500000);
  });

  it('rounds 999_999 into a four-digit k value instead of promoting to M', () => {
    // 999999 < 1_000_000 so the M branch is skipped; 999999/1000 = 999.999
    // which .toFixed(1) rounds to '1000.0'. Documents the seam between the
    // k and M buckets.
    expect(renderNumber(999999)).toBe('1000.0k');
  });
});

describe('renderNumberWithPoint', () => {
  it('returns an empty string for undefined', () => {
    expect(renderNumberWithPoint(undefined)).toBe('');
  });

  it('fixes to two decimals below the 100k shortening threshold', () => {
    expect(renderNumberWithPoint(1234.5)).toBe('1234.50');
    expect(renderNumberWithPoint(0)).toBe('0.00');
  });

  it('elides the middle of the integer part at or above 100k', () => {
    // '123456.79' -> whole '123456' -> '12' + '..' + '56' , decimals kept
    expect(renderNumberWithPoint(123456.789)).toBe('12..56.79');
    // exact threshold: '100000.00' -> '10' + '..' + '00' + '.00'
    expect(renderNumberWithPoint(100000)).toBe('10..00.00');
  });

  it('should not throw on null (DEFECT: only undefined is guarded)', () => {
    // render.jsx:892 guards `num === undefined` only, so a null quota coming
    // back from the API reaches null.toFixed(2) and throws a TypeError,
    // taking the whole table render down. Correct contract mirrors the
    // undefined case: return ''. Un-skip once the guard covers null.
    expect(renderNumberWithPoint(null)).toBe('');
  });
});

describe('renderQuotaNumberWithDigit', () => {
  it('prefixes $ under the default USD display type', () => {
    expect(renderQuotaNumberWithDigit(1.5)).toBe('$1.50');
    expect(renderQuotaNumberWithDigit(1.5, 4)).toBe('$1.5000');
  });

  it('prefixes the yuan sign under CNY', () => {
    localStorage.setItem('quota_display_type', 'CNY');
    expect(renderQuotaNumberWithDigit(2.25)).toBe('¥2.25');
  });

  it('uses the configured custom symbol under CUSTOM', () => {
    localStorage.setItem('quota_display_type', 'CUSTOM');
    localStorage.setItem(
      'status',
      JSON.stringify({ custom_currency_symbol: '₽' }),
    );
    expect(renderQuotaNumberWithDigit(3)).toBe('₽3.00');
  });

  it('falls back to the generic currency sign when status is unparsable', () => {
    localStorage.setItem('quota_display_type', 'CUSTOM');
    localStorage.setItem('status', 'not json{');
    expect(renderQuotaNumberWithDigit(3)).toBe('¤3.00');
  });

  it('emits a bare number under TOKENS (no currency sign)', () => {
    localStorage.setItem('quota_display_type', 'TOKENS');
    expect(renderQuotaNumberWithDigit(7)).toBe('7.00');
  });

  it('collapses non-numeric input to the number 0, not a money string', () => {
    // Documents the type seam: valid input yields a string like '$1.50',
    // invalid input yields the *number* 0 with no currency sign at all.
    expect(renderQuotaNumberWithDigit('1.5')).toBe(0);
    expect(renderQuotaNumberWithDigit(NaN)).toBe(0);
    expect(renderQuotaNumberWithDigit(undefined)).toBe(0);
  });
});

describe('quota unit conversion', () => {
  it('reads quota_per_unit as a float', () => {
    localStorage.setItem('quota_per_unit', UNIT);
    expect(getQuotaPerUnit()).toBe(500000);
  });

  it('renderUnitWithQuota multiplies quota by the unit rate', () => {
    localStorage.setItem('quota_per_unit', UNIT);
    // 2 USD * 500_000 units/USD = 1_000_000 units
    expect(renderUnitWithQuota(2)).toBe(1000000);
    expect(renderUnitWithQuota('0.5')).toBe(250000);
  });

  it('getQuotaWithUnit divides by the unit rate with 6 digits by default', () => {
    localStorage.setItem('quota_per_unit', UNIT);
    expect(getQuotaWithUnit(500000)).toBe('1.000000');
    expect(getQuotaWithUnit(250000, 2)).toBe('0.50');
    expect(getQuotaWithUnit(0)).toBe('0.000000');
  });

  it('should not surface NaN when quota_per_unit was never stored', () => {
    // DEFECT: localStorage.getItem returns null before /api/status lands (or
    // when the field is absent), parseFloat(null) is NaN, and every quota
    // divide becomes NaN. Users see the literal text 'NaN' where money goes.
    // Correct contract: fall back to the canonical 500_000 unit rate.
    expect(getQuotaWithUnit(500000)).toBe('1.000000');
  });

  it('hands the canonical unit rate to every unit helper when it is missing', () => {
    // Replaces the companion that pinned the NaN blast radius. Same three
    // helpers, same missing-config state, now stating the rate they must use.
    expect(getQuotaPerUnit()).toBe(500000);
    expect(renderUnitWithQuota(2)).toBe(1000000);
    expect(getQuotaWithUnit(250000, 2)).toBe('0.50');
  });

  it('shows a placeholder rather than a made-up figure when the rate is missing', () => {
    // renderQuota is what the quota column, the balance figures and the log
    // rows all call, and the tests below cannot see this state because their
    // describe block seeds quota_per_unit in beforeEach. It used to print the
    // literal '$NaN' here.
    //
    // The fix is deliberately NOT the 500000 fallback the three helpers above
    // use. quota_per_unit is operator-overridable, so computing from the
    // default would state a specific amount of money that is simply wrong on
    // any deployment that changed it — and wrong silently, which is worse than
    // visibly broken. q6_UsersColumnDefs.test.jsx locks that same invariant
    // from the UI side: the cell must never claim a concrete figure it could
    // not actually compute.
    const shown = renderQuota(500000);
    expect(shown).not.toMatch(/NaN/);
    expect(shown).not.toMatch(/\d/);
  });
});

describe('renderQuota', () => {
  beforeEach(() => {
    localStorage.setItem('quota_per_unit', UNIT);
  });

  it('converts quota units to USD with 2 decimals by default', () => {
    expect(renderQuota(500000)).toBe('$1.00');
    expect(renderQuota(1250000)).toBe('$2.50');
    expect(renderQuota(0)).toBe('$0.00');
  });

  it('honours an explicit digit count', () => {
    expect(renderQuota(500000, 4)).toBe('$1.0000');
    expect(renderQuota(1, 6)).toBe('$0.000002');
  });

  it('floors a positive sub-cent amount up to the smallest visible unit', () => {
    // 1 unit = $0.000002, which would render as $0.00 and read as "free".
    // The clamp promotes it to 10^-digits so a non-zero charge stays visible.
    expect(renderQuota(1)).toBe('$0.01');
    expect(renderQuota(1, 3)).toBe('$0.001');
  });

  it('passes raw units through renderNumber under TOKENS', () => {
    localStorage.setItem('quota_display_type', 'TOKENS');
    expect(renderQuota(1500000)).toBe('1.5M');
    expect(renderQuota(500)).toBe(500);
  });

  it('applies the configured exchange rate under CNY', () => {
    localStorage.setItem('quota_display_type', 'CNY');
    localStorage.setItem('status', JSON.stringify({ usd_exchange_rate: 7.2 }));
    // 500_000 units = $1 -> 1 * 7.2 = 7.20
    expect(renderQuota(500000)).toBe('¥7.20');
  });

  it('applies the configured custom rate and symbol under CUSTOM', () => {
    localStorage.setItem('quota_display_type', 'CUSTOM');
    localStorage.setItem(
      'status',
      JSON.stringify({
        custom_currency_symbol: '€',
        custom_currency_exchange_rate: 0.9,
      }),
    );
    expect(renderQuota(1000000)).toBe('€1.80');
  });

  it('should not render a signed zero for a tiny negative balance', () => {
    // DEFECT: -1 unit is -$0.000002; .toFixed(2) yields the string '-0.00'.
    // The sub-cent clamp at render.jsx:1040 only fires for quota > 0, so an
    // enterprise customer with a hair-thin negative balance is shown the
    // nonsense value '$-0.00'. Correct contract: '$0.00' (or a real -0.01).
    expect(renderQuota(-1)).toBe('$0.00');
  });

  it('renders larger negative balances normally', () => {
    expect(renderQuota(-500000)).toBe('$-1.00');
  });
});

describe('getCurrencyConfig vs renderQuota exchange-rate agreement', () => {
  it('reports USD/1 by default', () => {
    expect(getCurrencyConfig()).toEqual({ symbol: '$', rate: 1, type: 'USD' });
  });

  it('reads the configured CNY rate', () => {
    localStorage.setItem('quota_display_type', 'CNY');
    localStorage.setItem('status', JSON.stringify({ usd_exchange_rate: 7.3 }));
    expect(getCurrencyConfig()).toEqual({
      symbol: '¥',
      rate: 7.3,
      type: 'CNY',
    });
  });

  it('reads the configured custom symbol and rate', () => {
    localStorage.setItem('quota_display_type', 'CUSTOM');
    localStorage.setItem(
      'status',
      JSON.stringify({
        custom_currency_symbol: '元',
        custom_currency_exchange_rate: 6.5,
      }),
    );
    expect(getCurrencyConfig()).toEqual({
      symbol: '元',
      rate: 6.5,
      type: 'CUSTOM',
    });
  });

  it('should agree with renderQuota on the CNY fallback rate', () => {
    // DEFECT: with a status blob that has no usd_exchange_rate,
    // getCurrencyConfig (render.jsx:976) falls back to 7 while renderQuota
    // (render.jsx:1020) falls back to 1. The same $1 of spend is therefore
    // labelled ¥7.00 in the per-request price tooltip and ¥1.00 in the quota
    // column of the very same log row. One of the two is wrong; they must be
    // the same number.
    localStorage.setItem('quota_display_type', 'CNY');
    localStorage.setItem('quota_per_unit', UNIT);
    localStorage.setItem('status', JSON.stringify({}));
    const viaConfig = getCurrencyConfig().rate;
    const viaRenderQuota = Number(renderQuota(500000).slice(1));
    expect(viaRenderQuota).toBe(viaConfig);
  });

  it('prints $1 of spend as ¥7.00 on the CNY fallback rate', () => {
    // Replaces the companion that pinned the ¥1.00 half of the disagreement.
    // 500_000 units = $1; the shared fallback rate is 7, so the only correct
    // figure is ¥7.00.
    localStorage.setItem('quota_display_type', 'CNY');
    localStorage.setItem('quota_per_unit', UNIT);
    localStorage.setItem('status', JSON.stringify({}));
    expect(getCurrencyConfig().rate).toBe(7);
    expect(renderQuota(500000)).toBe('¥7.00');
  });

  it('applies a CNY rate even with no status blob at all', () => {
    // The yuan sign was set unconditionally while the rate stayed at its USD
    // initialiser of 1, so a customer whose cached status blob was missing or
    // corrupt saw their balance understated sevenfold — under a ¥ sign, on the
    // screen they use to decide whether to top up. Whether the blob exists
    // cannot be what decides the magnitude; a blob that merely omits the rate
    // already lands on 7.
    localStorage.setItem('quota_display_type', 'CNY');
    localStorage.setItem('quota_per_unit', UNIT);
    expect(getCurrencyConfig().rate).toBe(7);
    expect(renderQuota(500000)).toBe('¥7.00');
  });

  it('applies a CNY rate when the status blob is unparseable', () => {
    localStorage.setItem('quota_display_type', 'CNY');
    localStorage.setItem('quota_per_unit', UNIT);
    localStorage.setItem('status', 'not json{');
    expect(getCurrencyConfig().rate).toBe(7);
    expect(renderQuota(500000)).toBe('¥7.00');
  });

  it('should agree with renderQuota on the CUSTOM fallback symbol', () => {
    // DEFECT: with quota_display_type=CUSTOM but no status blob at all,
    // getCurrencyConfig leaves symbol as its '$' initialiser (the '¤'
    // default only lives inside the `if (statusStr)` block, render.jsx:983)
    // whereas renderQuota/renderQuotaWithAmount default to '¤'. A custom
    // currency thus shows as US dollars in price tooltips.
    localStorage.setItem('quota_display_type', 'CUSTOM');
    localStorage.setItem('quota_per_unit', UNIT);
    expect(getCurrencyConfig().symbol).toBe('¤');
  });

  it('renders the generic currency sign with no status blob at all', () => {
    // Replaces the companion that pinned the '$' half of the disagreement:
    // both helpers must reach for '¤' here, and $1 of spend stays 1 unit of
    // the custom currency because the fallback rate is 1.
    localStorage.setItem('quota_display_type', 'CUSTOM');
    localStorage.setItem('quota_per_unit', UNIT);
    expect(getCurrencyConfig().symbol).toBe('¤');
    expect(renderQuota(500000)).toBe('¤1.00');
  });
});

describe('convertUSDToCurrency', () => {
  it('is the identity (with a $ sign) under USD', () => {
    expect(convertUSDToCurrency(10)).toBe('$10.00');
    expect(convertUSDToCurrency(10, 4)).toBe('$10.0000');
  });

  it('multiplies by the CNY rate', () => {
    localStorage.setItem('quota_display_type', 'CNY');
    localStorage.setItem('status', JSON.stringify({ usd_exchange_rate: 7 }));
    expect(convertUSDToCurrency(10)).toBe('¥70.00');
  });

  it('keeps the sign of a refund', () => {
    expect(convertUSDToCurrency(-2.5)).toBe('$-2.50');
  });
});

describe('renderQuotaWithAmount', () => {
  it('prefixes $ under the default USD display type', () => {
    // Two decimals, matching renderQuota: this is the same channel-balance
    // column, where the used half already printed '¥7.00' beside a remaining
    // half of '¥7'. The magnitude is unchanged.
    expect(renderQuotaWithAmount(12.5)).toBe('$12.50');
  });

  it('converts to raw token units under TOKENS', () => {
    localStorage.setItem('quota_display_type', 'TOKENS');
    localStorage.setItem('quota_per_unit', UNIT);
    // 2 * 500_000 = 1_000_000 units -> renderNumber -> '1.0M'
    expect(renderQuotaWithAmount(2)).toBe('1.0M');
  });

  it('should convert the amount when the display currency is CNY', () => {
    // DEFECT: renderQuotaWithAmount (render.jsx:944) only concatenates the
    // yuan sign onto the USD figure -- no exchange rate is applied, unlike
    // renderQuota. Its single caller renders upstream channel balances
    // (ChannelsColumnDefs.jsx:448), so a $100 channel balance is displayed
    // as "¥100" on a CNY console: a ~7x understatement of a real balance.
    localStorage.setItem('quota_display_type', 'CNY');
    localStorage.setItem('status', JSON.stringify({ usd_exchange_rate: 7 }));
    expect(renderQuotaWithAmount(100)).toBe('¥700.00');
  });

  it('rounds the converted figure instead of printing float noise', () => {
    // 1.1 * 7.3 is 8.030000000000001 in binary floating point, and that is
    // exactly what the channel table rendered: a balance column showing
    // ¥8.030000000000001.
    localStorage.setItem('quota_display_type', 'CNY');
    localStorage.setItem('status', JSON.stringify({ usd_exchange_rate: 7.3 }));
    expect(renderQuotaWithAmount(1.1)).toBe('¥8.03');
    expect(renderQuotaWithAmount(2.9)).toBe('¥21.17');
  });

  it('applies the custom currency rate too, not just its symbol', () => {
    // Replaces the companion that pinned the unconverted '€100'. A $100
    // channel balance at 0.9 custom units per USD is 90 of them.
    localStorage.setItem('quota_display_type', 'CUSTOM');
    localStorage.setItem(
      'status',
      JSON.stringify({
        custom_currency_symbol: '€',
        custom_currency_exchange_rate: 0.9,
      }),
    );
    expect(renderQuotaWithAmount(100)).toBe('€90.00');
  });
});

describe('renderQuotaWithPrompt', () => {
  it('prefixes the equivalent-amount label outside TOKENS mode', () => {
    localStorage.setItem('quota_per_unit', UNIT);
    expect(renderQuotaWithPrompt(500000, 2)).toBe('等价金额：$1.00');
  });

  it('is empty under TOKENS (the raw number already is the quota)', () => {
    localStorage.setItem('quota_display_type', 'TOKENS');
    expect(renderQuotaWithPrompt(500000, 2)).toBe('');
  });
});

describe('renderText truncation', () => {
  it('leaves text at or below the limit alone', () => {
    expect(renderText('hello', 10)).toBe('hello');
    expect(renderText('hello', 5)).toBe('hello');
  });

  it('truncates to exactly `limit` characters including the ellipsis', () => {
    // 'abcdefghij'.slice(0, 5-3) = 'ab' -> 'ab...' is 5 chars.
    expect(renderText('abcdefghij', 5)).toBe('ab...');
    expect(renderText('abcdefghij', 5)).toHaveLength(5);
    expect(renderText('abcdefghij', 8)).toBe('abcde...');
  });

  it('should never return a string longer than the input', () => {
    // DEFECT: for limit < 3 the expression text.slice(0, limit - 3) gets a
    // NEGATIVE end index, so slice counts from the end and keeps almost the
    // whole string -- then appends '...'. renderText('abcdef', 2) returns
    // 'abcde...' (8 chars) for a requested limit of 2: longer than both the
    // limit and the input, which is the opposite of truncating.
    const out = renderText('abcdef', 2);
    expect(out.length).toBeLessThanOrEqual(6);
  });

  it('degenerates to a bare ellipsis once the limit leaves no room for text', () => {
    // Replaces the companion that pinned the expanding behaviour. limit 3 is
    // the boundary; below it the slice end is clamped at 0 rather than going
    // negative, so nothing survives but the ellipsis.
    expect(renderText('abcdef', 3)).toBe('...');
    expect(renderText('abcdef', 2)).toBe('...');
    expect(renderText('abcdef', 0)).toBe('...');
  });
});

describe('colour derivation', () => {
  it('stringToColor indexes the 15-name palette by char-code sum', () => {
    // 'ab' -> 97 + 98 = 195; 195 % 15 === 0 -> first palette entry.
    expect(stringToColor('ab')).toBe('amber');
    // Empty string sums to 0 -> same first entry.
    expect(stringToColor('')).toBe('amber');
    // 'acme-tenant' sums to 1101; 1101 % 15 === 6 -> seventh entry.
    expect(stringToColor('acme-tenant')).toBe('light-blue');
  });

  it('stringToColor sums char codes, so anagrams collide', () => {
    expect(stringToColor('ab')).toBe(stringToColor('ba'));
    expect(stringToColor('vip')).toBe(stringToColor('piv'));
  });

  it('modelToColor prefers the curated map', () => {
    expect(modelToColor('gpt-4')).toBe(modelColorMap['gpt-4']);
    expect(modelToColor('claude-2.1')).toBe('rgb(255,209,190)');
  });

  it('modelToColor hashes unknown models to a stable palette entry', () => {
    const c = modelToColor('some-unlisted-model-v9');
    expect(c).toBe(modelToColor('some-unlisted-model-v9'));
    expect(typeof c).toBe('string');
    expect(c.startsWith('#') || c.startsWith('rgb')).toBe(true);
  });

  it('should return a colour string for a model named like an Object key', () => {
    // DEFECT: modelColorMap is a plain object literal, so the membership
    // test `if (modelColorMap[modelName])` at render.jsx:578 hits the
    // prototype chain. A model or tag literally named 'constructor' /
    // 'toString' returns an inherited *function* instead of a colour, which
    // React then tries to use as a Tag `color` prop. Correct contract: use
    // Object.prototype.hasOwnProperty / a null-prototype map.
    expect(typeof modelToColor('constructor')).toBe('string');
  });

  it('hashes prototype-named models like any other unlisted model', () => {
    // Replaces the companion that pinned the leaked Object.prototype members.
    for (const name of ['constructor', 'toString', 'hasOwnProperty']) {
      const c = modelToColor(name);
      expect(typeof c).toBe('string');
      expect(c.startsWith('#') || c.startsWith('rgb')).toBe(true);
    }
  });
});
