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
import { render, screen } from '@testing-library/react';

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

vi.mock('@douyinfe/semi-ui', () => {
  const passthrough = ({ children, ...rest }) =>
    React.createElement('span', rest, children);
  return {
    Tag: passthrough,
    Typography: { Text: passthrough },
    Avatar: passthrough,
    Modal: { error: vi.fn() },
  };
});

// Deterministic i18n: keys are echoed with {{var}} interpolation, which is
// exactly what i18next does for these (untranslated) zh source strings. That
// keeps every assertion below about the ARITHMETIC, not about catalogues.
vi.mock('i18next', () => {
  const stub = {
    language: 'zh',
    t: (key, vars) =>
      vars
        ? String(key).replace(/\{\{(\w+)\}\}/g, (_, name) =>
            vars[name] === undefined ? `{{${name}}}` : String(vars[name]),
          )
        : key,
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
  renderLogContent,
  renderModelPriceSimple,
  renderModelPrice,
  renderClaudeModelPrice,
  renderClaudeLogContent,
  renderAudioModelPrice,
  rehypeSplitWordsIntoSpans,
} from './render';

beforeEach(() => {
  localStorage.clear();
});

// Renders the JSX price tooltips and returns their flattened text.
const textOf = (node) => {
  const { container } = render(<div>{node}</div>);
  return container.textContent;
};

describe('renderLogContent', () => {
  it('per-call pricing shows the flat price and the group ratio', () => {
    expect(renderLogContent(2, 3, 0.05, 1)).toBe(
      '模型价格 $0.050000，分组倍率 1',
    );
  });

  it('per-token pricing lists model / cache / output / group ratios', () => {
    expect(renderLogContent(2, 3, -1, 1.5, undefined, 0.5)).toBe(
      '模型倍率 2，缓存倍率 0.5，输出倍率 3，分组倍率 1.5',
    );
  });

  it('adds the image ratio when the request had image input', () => {
    expect(renderLogContent(2, 3, -1, 1, undefined, 0.5, true, 4)).toBe(
      '模型倍率 2，缓存倍率 0.5，输出倍率 3，图片输入倍率 4，分组倍率 1',
    );
  });

  it('adds the web-search call count when web search was used', () => {
    expect(
      renderLogContent(2, 3, -1, 1, undefined, 0.5, false, 1, true, 7),
    ).toBe(
      '模型倍率 2，缓存倍率 0.5，输出倍率 3，分组倍率 1，Web 搜索调用 7 次',
    );
  });

  it('image input wins over web search when both flags are set', () => {
    const out = renderLogContent(2, 3, -1, 1, undefined, 0.5, true, 4, true, 7);
    expect(out).toContain('图片输入倍率 4');
    expect(out).not.toContain('Web 搜索调用');
  });

  it('a per-user override replaces the group ratio and relabels it', () => {
    expect(renderLogContent(2, 3, -1, 1.5, 0.25, 0.5)).toBe(
      '模型倍率 2，缓存倍率 0.5，输出倍率 3，专属倍率 0.25',
    );
  });

  it('treats -1 as "no per-user override" and keeps the group ratio', () => {
    expect(renderLogContent(2, 3, -1, 1.5, -1, 0.5)).toBe(
      '模型倍率 2，缓存倍率 0.5，输出倍率 3，分组倍率 1.5',
    );
  });

  it('converts the per-call price into the display currency', () => {
    localStorage.setItem('quota_display_type', 'CNY');
    localStorage.setItem('status', JSON.stringify({ usd_exchange_rate: 7 }));
    // 0.05 USD * 7 = 0.35
    expect(renderLogContent(2, 3, 0.05, 1)).toBe(
      '模型价格 ¥0.350000，分组倍率 1',
    );
  });
});

describe('renderModelPriceSimple', () => {
  it('per-call pricing shows price times ratio', () => {
    expect(renderModelPriceSimple(2, 0.05, 3)).toBe(
      '价格：$0.050000 * 分组倍率：3',
    );
  });

  it('per-token pricing with no cache lists only model and group ratio', () => {
    expect(renderModelPriceSimple(2, -1, 1)).toBe('模型: 2 * 分组倍率: 1');
  });

  it('adds a cache segment once cache tokens were read', () => {
    expect(renderModelPriceSimple(2, -1, 1, undefined, 100, 0.5)).toBe(
      '模型: 2 * 缓存: 0.5 * 分组倍率: 1',
    );
  });

  it('adds the legacy cache-creation segment when no split buckets exist', () => {
    expect(renderModelPriceSimple(2, -1, 1, undefined, 0, 1, 500, 1.25)).toBe(
      '模型: 2 * 缓存创建: 1.25 * 分组倍率: 1',
    );
  });

  it('prefers the split 5m/1h buckets over the legacy one', () => {
    expect(
      renderModelPriceSimple(
        2,
        -1,
        1,
        undefined,
        0,
        1,
        500,
        1.25,
        100,
        1.5,
        200,
        2,
      ),
    ).toBe('模型: 2 * 缓存创建: 5m 1.5 / 1h 2 * 分组倍率: 1');
  });

  it('shows only the 5m bucket when 1h had no tokens', () => {
    expect(
      renderModelPriceSimple(2, -1, 1, undefined, 0, 1, 0, 1, 100, 1.5, 0, 2),
    ).toBe('模型: 2 * 缓存创建: 5m 1.5 * 分组倍率: 1');
  });

  it('shows only the 1h bucket when 5m had no tokens', () => {
    expect(
      renderModelPriceSimple(2, -1, 1, undefined, 0, 1, 0, 1, 0, 1.5, 200, 2),
    ).toBe('模型: 2 * 缓存创建: 1h 2 * 分组倍率: 1');
  });

  it('adds an image segment when the flag is set', () => {
    expect(
      renderModelPriceSimple(
        2,
        -1,
        1,
        undefined,
        0,
        1,
        0,
        1,
        0,
        1,
        0,
        1,
        true,
        3,
      ),
    ).toBe('模型: 2 * 图片输入: 3 * 分组倍率: 1');
  });

  it('appends the system-prompt-override notice on its own line', () => {
    const out = renderModelPriceSimple(
      2,
      -1,
      1,
      undefined,
      0,
      1,
      0,
      1,
      0,
      1,
      0,
      1,
      false,
      1,
      true,
    );
    expect(out).toBe('模型: 2 * 分组倍率: 1\n\r系统提示覆盖');
  });

  it('labels a per-user override as such', () => {
    expect(renderModelPriceSimple(2, -1, 1, 0.25)).toBe(
      '模型: 2 * 专属倍率: 0.25',
    );
  });
});

describe('renderModelPrice (OpenAI-style tooltip)', () => {
  it('per-call pricing multiplies price by the group ratio', () => {
    expect(renderModelPrice(0, 0, 0, 0.05, 0, 2)).toBe(
      '模型价格：$0.050000 * 分组倍率：2 = $0.100000',
    );
  });

  it('per-token: 1 ratio unit is $2 / 1M tokens and the total is the sum', () => {
    // in 1M @ ratio 1 -> $2/1M; out 0.5M @ completionRatio 3 -> $6/1M
    // total = (1 * 2 + 0.5 * 6) * groupRatio 2 = 10
    const text = textOf(renderModelPrice(1_000_000, 500_000, 1, -1, 3, 2));
    expect(text).toContain('输入价格：$2.000000 / 1M tokens');
    expect(text).toContain(
      '输出价格：$2.000000 * 3 = $6.000000 / 1M tokens (补全倍率: 3)',
    );
    expect(text).toContain(
      '(输入 1000000 tokens / 1M tokens * $2.000000 + 输出 500000 tokens / 1M tokens * $6.000000) * 分组倍率 2 = $10.000000',
    );
    expect(text).toContain('仅供参考，以实际扣费为准');
  });

  it('per-token: a missing completion ratio is charged as zero, not NaN', () => {
    const text = textOf(
      renderModelPrice(1_000_000, 500_000, 1, -1, undefined, 1),
    );
    expect(text).not.toContain('NaN');
    // only the input leg is charged: 1M * $2/1M * 1 = $2
    expect(text).toContain('= $2.000000');
  });

  it('cached input is discounted and itemised', () => {
    // 1M input of which 200k cached at ratio 0.5
    // effective = 1M - 200k + 200k*0.5 = 900k -> 0.9*2*2 = 3.6
    // output 0.5M * $6/1M * 2 = 6 ; total 9.6
    const text = textOf(
      renderModelPrice(
        1_000_000,
        500_000,
        1,
        -1,
        3,
        2,
        undefined,
        200_000,
        0.5,
      ),
    );
    expect(text).toContain(
      '缓存价格：$2.000000 * 0.5 = $1.000000 / 1M tokens (缓存倍率: 0.5)',
    );
    expect(text).toContain(
      '(输入 800000 tokens / 1M tokens * $2.000000 + 缓存 200000 tokens / 1M tokens * $1.000000',
    );
    expect(text).toContain('= $9.600000');
  });

  it('image input is surcharged and itemised', () => {
    // 1M input of which 200k image at ratio 2
    // effective = 1M - 200k + 200k*2 = 1.2M -> 1.2*2*1 = 2.4
    const text = textOf(
      renderModelPrice(
        1_000_000,
        0,
        1,
        -1,
        0,
        1,
        undefined,
        0,
        1,
        true,
        2,
        200_000,
      ),
    );
    expect(text).toContain(
      '图片输入价格：$4.000000 * 1 = $4.000000 / 1M tokens (图片倍率: 2)',
    );
    expect(text).toContain(
      '(输入 800000 tokens + 图片输入 200000 tokens * 2 / 1M tokens * $2.000000',
    );
    expect(text).toContain('= $2.400000');
  });

  it('web-search calls are billed per 1K calls', () => {
    // 2000 calls / 1K * $0.03 * ratio 1 = $0.06
    const text = textOf(
      renderModelPrice(
        0,
        0,
        1,
        -1,
        0,
        1,
        undefined,
        0,
        1,
        false,
        1,
        0,
        true,
        2000,
        0.03,
      ),
    );
    expect(text).toContain('Web搜索价格：$0.030000 / 1K 次');
    expect(text).toContain(
      ' + Web搜索 2000次 / 1K 次 * $0.030000 * 分组倍率 1',
    );
    expect(text).toContain('= $0.060000');
  });

  it('file-search calls are billed per 1K calls', () => {
    // 1000 calls / 1K * $0.05 * ratio 1 = $0.05
    const text = textOf(
      renderModelPrice(
        0,
        0,
        1,
        -1,
        0,
        1,
        undefined,
        0,
        1,
        false,
        1,
        0,
        false,
        0,
        0,
        true,
        1000,
        0.05,
      ),
    );
    expect(text).toContain('文件搜索价格：$0.050000 / 1K 次');
    expect(text).toContain('= $0.050000');
  });

  it('separately-priced audio input is carved out of the text input', () => {
    // 1M input, 100k of it audio at $8/1M; text leg = 900k * $2/1M = 1.8
    // audio leg = 100k * $8/1M = 0.8 ; total 2.6
    const text = textOf(
      renderModelPrice(
        1_000_000,
        0,
        1,
        -1,
        0,
        1,
        undefined,
        0,
        1,
        false,
        1,
        0,
        false,
        0,
        0,
        false,
        0,
        0,
        true,
        100_000,
        8,
      ),
    );
    expect(text).toContain(
      '输入价格：$2.000000 / 1M tokens，音频 $8.000000 / 1M tokens',
    );
    expect(text).toContain(
      '(输入 900000 tokens / 1M tokens * $2.000000 + 音频输入 100000 tokens / 1M tokens * $8.000000',
    );
    expect(text).toContain('= $2.600000');
  });

  it('an image-generation call is billed once per call', () => {
    const text = textOf(
      renderModelPrice(
        0,
        0,
        1,
        -1,
        0,
        1,
        undefined,
        0,
        1,
        false,
        1,
        0,
        false,
        0,
        0,
        false,
        0,
        0,
        false,
        0,
        0,
        true,
        0.1,
      ),
    );
    expect(text).toContain('图片生成调用：$0.100000 / 1次');
    expect(text).toContain(' + 图片生成调用 $0.100000 / 1次 * 分组倍率 1');
    expect(text).toContain('= $0.100000');
  });

  it('should not charge for an image-generation call the flag says did not happen', () => {
    // DEFECT: `imageGenerationCallPrice * groupRatio` is added to the total
    // unconditionally (render.jsx:1241) while BOTH the itemised <p> and the
    // breakdown string are gated on `imageGenerationCall && price > 0`
    // (render.jsx:1312 and 1406). With the flag false but a non-zero price
    // on the log row, the tooltip shows a total that none of its own line
    // items add up to -- the classic "where did that money come from" bug.
    const text = textOf(
      renderModelPrice(
        0,
        0,
        1,
        -1,
        0,
        1,
        undefined,
        0,
        1,
        false,
        1,
        0,
        false,
        0,
        0,
        false,
        0,
        0,
        false,
        0,
        0,
        false,
        0.1,
      ),
    );
    // Must target the TOTAL specifically. `toContain('= $0.000000')` is not
    // enough: the itemised output-price line is literally
    // "输出价格：$2.000000 * 0 = $0.000000", so that substring is present even
    // while the defect is live — the check passes either way and would give a
    // false all-clear the moment someone un-skips this after a fix. The total
    // is the figure that closes the breakdown line (".. * 分组倍率 1 = ..") and
    // is immediately followed by the disclaimer, so anchor on both edges.
    expect(text).not.toContain('= $0.100000');
    expect(text).toMatch(/\* 分组倍率 1 = \$0\.000000仅供参考/);
  });

  it('converts every figure into the display currency', () => {
    localStorage.setItem('quota_display_type', 'CNY');
    localStorage.setItem('status', JSON.stringify({ usd_exchange_rate: 7 }));
    const text = textOf(renderModelPrice(1_000_000, 0, 1, -1, 0, 1));
    // $2/1M becomes ¥14/1M; the $2 total becomes ¥14
    expect(text).toContain('输入价格：¥14.000000 / 1M tokens');
    expect(text).toContain('= ¥14.000000');
  });
});

describe('renderClaudeModelPrice', () => {
  it('per-call pricing multiplies price by the group ratio', () => {
    expect(renderClaudeModelPrice(0, 0, 0, 0.05, 0, 2)).toBe(
      '模型价格：$0.050000 * 分组倍率：2 = $0.100000',
    );
  });

  it('adds cache reads to the input instead of subtracting them', () => {
    // Anthropic reports input_tokens EXCLUDING cache reads, so unlike the
    // OpenAI variant the cached tokens are added on top:
    // effective = 1M + 200k*0.5 = 1.1M -> 1.1*2*2 = 4.4 ; output 0.5M*6*2 = 6
    const text = textOf(
      renderClaudeModelPrice(
        1_000_000,
        500_000,
        1,
        -1,
        3,
        2,
        undefined,
        200_000,
        0.5,
      ),
    );
    expect(text).toContain(
      '缓存价格：$2.000000 * 0.5 = $1.000000 / 1M tokens (缓存倍率: 0.5)',
    );
    expect(text).toContain(
      '提示 1000000 tokens / 1M tokens * $2.000000 + 缓存 200000 tokens / 1M tokens * $1.000000 (倍率: 0.5) + 补全 500000 tokens / 1M tokens * $6.000000 * 分组倍率 2 = $10.400000',
    );
  });

  it('bills a plain request with no cache at all', () => {
    const text = textOf(
      renderClaudeModelPrice(1_000_000, 500_000, 1, -1, 3, 2),
    );
    expect(text).toContain('提示价格：$2.000000 / 1M tokens');
    expect(text).toContain('补全价格：$2.000000 * 3 = $6.000000 / 1M tokens');
    expect(text).toContain(
      '提示 1000000 tokens / 1M tokens * $2.000000 + 补全 500000 tokens / 1M tokens * $6.000000 * 分组倍率 2 = $10.000000',
    );
    expect(text).not.toContain('缓存创建');
  });

  it('bills legacy (unsplit) cache creation', () => {
    // effective = 1M + 100k*1.25 = 1.125M -> 1.125*2*1 = 2.25
    const text = textOf(
      renderClaudeModelPrice(
        1_000_000,
        0,
        1,
        -1,
        0,
        1,
        undefined,
        0,
        1,
        100_000,
        1.25,
      ),
    );
    expect(text).toContain(
      '缓存创建价格：$2.000000 * 1.25 = $2.500000 / 1M tokens (缓存创建倍率: 1.25)',
    );
    expect(text).toContain('= $2.250000');
  });

  it('bills the split 5m and 1h cache-creation buckets and their sum', () => {
    // effective = 1M + 100k*1.25 + 50k*2 = 1.225M -> 1.225*2*2 = 4.9
    // output 0.5M * $6/1M * 2 = 6 ; total 10.9
    const text = textOf(
      renderClaudeModelPrice(
        1_000_000,
        500_000,
        1,
        -1,
        3,
        2,
        undefined,
        0,
        1,
        999_999,
        9,
        100_000,
        1.25,
        50_000,
        2,
      ),
    );
    expect(text).toContain(
      '5m缓存创建价格：$2.000000 * 1.25 = $2.500000 / 1M tokens (5m缓存创建倍率: 1.25)',
    );
    expect(text).toContain(
      '1h缓存创建价格：$2.000000 * 2 = $4.000000 / 1M tokens (1h缓存创建倍率: 2)',
    );
    expect(text).toContain(
      '缓存创建价格合计：5m $2.500000 + 1h $4.000000 = $6.500000 / 1M tokens',
    );
    // The legacy bucket (999_999 tokens at ratio 9) must be ignored entirely
    // once split buckets are present, or the customer is billed twice: it
    // must appear neither as a line item nor in the total.
    expect(text).not.toContain('999999');
    expect(text).not.toContain('(倍率: 9)');
    expect(text).toContain('= $10.900000');
  });
});

describe('renderClaudeLogContent', () => {
  it('per-call pricing shows price and group ratio', () => {
    expect(renderClaudeLogContent(2, 3, 0.05, 1.5)).toBe(
      '模型价格 $0.050000，分组倍率 1.5',
    );
  });

  it('per-token pricing joins every ratio with a full-width comma', () => {
    expect(renderClaudeLogContent(2, 3, -1, 1.5, undefined, 0.5, 1.25)).toBe(
      '模型倍率 2，输出倍率 3，缓存倍率 0.5，缓存创建倍率 1.25，分组倍率 1.5',
    );
  });

  it('splits the cache-creation ratio when both buckets have tokens', () => {
    expect(
      renderClaudeLogContent(2, 3, -1, 1, undefined, 1, 1.25, 10, 1.5, 20, 2),
    ).toBe(
      '模型倍率 2，输出倍率 3，缓存倍率 1，缓存创建倍率 5m 1.5 / 1h 2，分组倍率 1',
    );
  });

  it('shows a single bucket when only one has tokens', () => {
    expect(
      renderClaudeLogContent(2, 3, -1, 1, undefined, 1, 1.25, 10, 1.5, 0, 2),
    ).toBe(
      '模型倍率 2，输出倍率 3，缓存倍率 1，缓存创建倍率 5m 1.5，分组倍率 1',
    );
    expect(
      renderClaudeLogContent(2, 3, -1, 1, undefined, 1, 1.25, 0, 1.5, 20, 2),
    ).toBe('模型倍率 2，输出倍率 3，缓存倍率 1，缓存创建倍率 1h 2，分组倍率 1');
  });

  it('labels a per-user override', () => {
    expect(renderClaudeLogContent(2, 3, -1, 1.5, 0.25)).toBe(
      '模型倍率 2，输出倍率 3，缓存倍率 1，缓存创建倍率 1，专属倍率 0.25',
    );
  });
});

describe('renderAudioModelPrice', () => {
  it('per-call pricing multiplies price by the group ratio', () => {
    expect(renderAudioModelPrice(0, 0, 0, 0.05, 0, 0, 0, 1, 1, 2)).toBe(
      '模型价格：$0.050000 * 分组倍率：2 = $0.100000',
    );
  });

  it('bills the text and audio legs separately and sums them', () => {
    // text  : 1M * $2/1M + 0.5M * $6/1M                    = 2 + 3 = 5
    // audio : 100k * $2/1M * 2 + 50k * $2/1M * 2 * 4       = 0.4 + 0.8 = 1.2
    const text = textOf(
      renderAudioModelPrice(
        1_000_000,
        500_000,
        1,
        -1,
        3,
        100_000,
        50_000,
        2,
        4,
        1,
      ),
    );
    expect(text).toContain('提示价格：$2.000000 / 1M tokens');
    expect(text).toContain(
      '音频提示 100000 tokens / 1M tokens * $4.000000 + 音频补全 50000 tokens / 1M tokens * $16.000000 = $1.200000',
    );
    expect(text).toContain(
      '总价：文字价格 5.000000 + 音频价格 1.200000 = $6.200000',
    );
    expect(text).toContain('仅供参考，以实际扣费为准');
  });

  it('discounts cached text input', () => {
    // effective text input = 1M - 200k + 200k*0.5 = 900k -> $1.8
    const text = textOf(
      renderAudioModelPrice(
        1_000_000,
        0,
        1,
        -1,
        0,
        0,
        0,
        1,
        1,
        1,
        undefined,
        200_000,
        0.5,
      ),
    );
    expect(text).toContain('总价：文字价格 1.800000 + 音频价格 0.000000');
  });
});

describe('rehypeSplitWordsIntoSpans', () => {
  const paragraph = (value) => ({
    type: 'root',
    children: [
      { type: 'element', tagName: 'p', children: [{ type: 'text', value }] },
    ],
  });

  it('splits a text node into one span per word segment', () => {
    const tree = paragraph('hello world');
    rehypeSplitWordsIntoSpans()(tree);
    const spans = tree.children[0].children;
    expect(spans.map((s) => s.children[0].value).join('')).toBe('hello world');
    expect(spans.every((s) => s.tagName === 'span')).toBe(true);
    expect(spans.length).toBeGreaterThan(1);
  });

  it('animates every segment when nothing was rendered before', () => {
    const tree = paragraph('hello world');
    rehypeSplitWordsIntoSpans({ previousContentLength: 0 })(tree);
    const spans = tree.children[0].children;
    expect(
      spans.every((s) => s.properties.className.includes('animate-fade-in')),
    ).toBe(true);
  });

  it('animates only the segments past the already-rendered prefix', () => {
    const tree = paragraph('hello world');
    // 'hello' occupies offsets 0..4, so with 5 chars already on screen only
    // what follows should fade in.
    rehypeSplitWordsIntoSpans({ previousContentLength: 5 })(tree);
    const spans = tree.children[0].children;
    expect(spans[0].children[0].value).toBe('hello');
    expect(spans[0].properties.className).toEqual([]);
    expect(
      spans.slice(1).every((s) => s.properties.className.length === 1),
    ).toBe(true);
  });

  it('rewrites headings and list items too, but leaves other tags alone', () => {
    const tree = {
      type: 'root',
      children: [
        {
          type: 'element',
          tagName: 'h2',
          children: [{ type: 'text', value: 'Title' }],
        },
        {
          type: 'element',
          tagName: 'code',
          children: [{ type: 'text', value: 'x=1' }],
        },
      ],
    };
    rehypeSplitWordsIntoSpans()(tree);
    expect(tree.children[0].children[0].tagName).toBe('span');
    expect(tree.children[1].children[0].type).toBe('text');
  });

  it('passes non-text children through untouched', () => {
    const img = { type: 'element', tagName: 'img', children: [] };
    const tree = {
      type: 'root',
      children: [
        {
          type: 'element',
          tagName: 'p',
          children: [img, { type: 'text', value: 'cap' }],
        },
      ],
    };
    rehypeSplitWordsIntoSpans()(tree);
    expect(tree.children[0].children[0]).toBe(img);
  });

  it('is a no-op for an element with no children array', () => {
    const tree = {
      type: 'root',
      children: [{ type: 'element', tagName: 'p' }],
    };
    expect(() => rehypeSplitWordsIntoSpans()(tree)).not.toThrow();
    expect(tree.children[0].children).toBeUndefined();
  });
});
