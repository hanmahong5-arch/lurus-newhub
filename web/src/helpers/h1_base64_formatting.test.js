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
import { describe, it, expect, afterEach, vi } from 'vitest';

// Echo keys so the relative-time bucketing stays independent of i18n init.
vi.mock('../i18n/i18n', () => ({ default: { t: (k) => k } }));

import { encodeToBase64 } from './base64';
import { formatClockTime, formatTime, quotaToUSD } from './formatting';

// Base64 of 'hi 中' — computed from the UTF-8 bytes
// 68 69 20 E4 B8 AD, i.e. the encoder must go through UTF-8, not charCodes.
const HI_ZH = 'hi 中';
const HI_ZH_B64 = 'aGkg5Lit';

describe('encodeToBase64 environment fallbacks', () => {
  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it('uses window.btoa over UTF-8 bytes in a browser', () => {
    expect(encodeToBase64(HI_ZH)).toBe(HI_ZH_B64);
  });

  it('produces the same bytes through the Buffer path off-window', () => {
    vi.stubGlobal('window', undefined);
    expect(encodeToBase64(HI_ZH)).toBe(HI_ZH_B64);
  });

  it('produces the same bytes through the global btoa path', () => {
    vi.stubGlobal('window', undefined);
    vi.stubGlobal('Buffer', undefined);
    expect(encodeToBase64(HI_ZH)).toBe(HI_ZH_B64);
  });

  it('still round-trips without TextEncoder (percent-escape fallback)', () => {
    vi.stubGlobal('window', undefined);
    vi.stubGlobal('Buffer', undefined);
    vi.stubGlobal('TextEncoder', undefined);
    expect(encodeToBase64(HI_ZH)).toBe(HI_ZH_B64);
  });

  it('throws a named error when no encoder exists at all', () => {
    vi.stubGlobal('window', undefined);
    vi.stubGlobal('Buffer', undefined);
    vi.stubGlobal('btoa', undefined);
    expect(() => encodeToBase64('x')).toThrow(
      'Base64 encoding is unavailable in the current environment',
    );
  });
});

describe('formatClockTime', () => {
  it('renders a fixed-width 24h wall clock with millisecond precision', () => {
    // Built from a LOCAL Date so the expectation holds in any timezone.
    const ts = new Date(2024, 0, 5, 7, 8, 9, 250).getTime() / 1000;
    expect(formatClockTime(ts)).toMatch(/^07:08:09[.,]250$/);
  });

  it('keeps the 24h layout past noon regardless of host locale', () => {
    const ts = new Date(2024, 0, 5, 23, 59, 58, 1).getTime() / 1000;
    expect(formatClockTime(ts)).toMatch(/^23:59:58[.,]001$/);
  });

  it('em-dashes an empty timestamp', () => {
    expect(formatClockTime(0)).toBe('—');
    expect(formatClockTime(undefined)).toBe('—');
  });
});

describe('formatTime', () => {
  it('renders a real timestamp as a locale date-time (not an em dash)', () => {
    const d = new Date(2024, 0, 5, 7, 8, 9);
    const out = formatTime(Math.floor(d.getTime() / 1000));
    expect(out).not.toBe('—');
    expect(out).toBe(d.toLocaleString());
  });
});

describe('quotaToUSD rounding', () => {
  it('rounds to the requested precision rather than truncating', () => {
    // 1 quota unit = $0.000002 -> at 5 digits that rounds UP to 0.00000
    expect(quotaToUSD(1, 5)).toBe('0.00000');
    // 250_000 units = exactly $0.50
    expect(quotaToUSD(250_000)).toBe('0.50');
    // negatives keep their sign
    expect(quotaToUSD(-500_000)).toBe('-1.00');
  });
});
