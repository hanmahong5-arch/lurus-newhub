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
import { render, screen, fireEvent, waitFor } from '@testing-library/react';

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

// Semi UI's barrel needs canvas (lottie-web). The Tag stand-in surfaces the
// props render.jsx chooses -- colour, shape, prefix icon -- as attributes so
// they can be asserted directly; that choice IS the logic under test.
vi.mock('@douyinfe/semi-ui', () => {
  const Tag = ({ children, color, shape, prefixIcon, suffixIcon, onClick }) =>
    React.createElement(
      'span',
      {
        'data-testid': 'tag',
        'data-color': color,
        'data-shape': shape,
        'data-has-prefix': prefixIcon ? 'yes' : 'no',
        'data-has-suffix': suffixIcon ? 'yes' : 'no',
        onClick,
      },
      children,
    );
  return {
    Tag,
    Typography: {
      Text: ({ children, ...rest }) =>
        React.createElement('span', rest, children),
    },
    Avatar: ({ children, size }) =>
      React.createElement(
        'span',
        { 'data-testid': 'avatar', 'data-size': size },
        children,
      ),
    Modal: { error: vi.fn() },
  };
});

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

// render.jsx only pulls `copy` and `showSuccess` out of utils; replace both
// so the group-tag click path is observable without a real clipboard.
vi.mock('./utils', async (importOriginal) => {
  const actual = await importOriginal();
  return {
    ...actual,
    copy: vi.fn().mockResolvedValue(true),
    showSuccess: vi.fn(),
  };
});

import { Modal } from '@douyinfe/semi-ui';
import { OpenAI, Claude } from '@lobehub/icons';
import { Key, CircleUser } from 'lucide-react';
import { copy, showSuccess } from './utils';
import {
  getModelCategories,
  getChannelIcon,
  getLucideIcon,
  getLobeHubIcon,
  renderModelTag,
  renderGroup,
  renderRatio,
  truncateText,
} from './render';

// Which category a model name lands in — first match wins over the insertion
// order of the category map, so this doubles as an ordering assertion.
const categorize = (name) => {
  const cats = getModelCategories((k) => k);
  return (
    Object.entries(cats).find(
      ([key, c]) => key !== 'all' && c.filter({ model_name: name }),
    )?.[0] ?? null
  );
};

beforeEach(() => {
  vi.clearAllMocks();
});

// NOTE: this describe must run FIRST — getModelCategories memoizes on first
// call, so any later call cannot observe the caching behaviour.
describe('getModelCategories memoization', () => {
  it('reuses the cached map, ignoring a new translator for the same locale', () => {
    const first = getModelCategories((k) => `A:${k}`);
    expect(first.all.label).toBe('A:全部模型');

    const second = getModelCategories((k) => `B:${k}`);
    // Same object identity, and the labels are still the FIRST translator's.
    expect(second).toBe(first);
    expect(second.all.label).toBe('A:全部模型');
  });

  it('the "all" pseudo-category matches every model', () => {
    const cats = getModelCategories((k) => k);
    expect(cats.all.filter({ model_name: 'anything-at-all' })).toBe(true);
    expect(cats.all.icon).toBeNull();
  });
});

describe('model → vendor categorisation', () => {
  it('routes flagship models to their own vendor', () => {
    expect(categorize('gpt-4o')).toBe('openai');
    expect(categorize('o3-mini')).toBe('openai');
    expect(categorize('whisper-1')).toBe('openai');
    expect(categorize('text-embedding-ada-002')).toBe('openai');
    expect(categorize('claude-3-5-sonnet-20241022')).toBe('anthropic');
    expect(categorize('gemini-2.0-flash')).toBe('gemini');
    expect(categorize('gemma-2-27b')).toBe('gemini');
    expect(categorize('kimi-k2')).toBe('moonshot');
    expect(categorize('glm-4.5')).toBe('zhipu');
    expect(categorize('qwen-max')).toBe('qwen');
    expect(categorize('deepseek-chat')).toBe('deepseek');
    expect(categorize('abab6.5-chat')).toBe('minimax');
    expect(categorize('ernie-4.0-8k')).toBe('baidu');
    expect(categorize('spark-max')).toBe('xunfei');
    expect(categorize('mj_imagine')).toBe('midjourney');
    expect(categorize('hunyuan-pro')).toBe('tencent');
    expect(categorize('command-r-plus')).toBe('cohere');
    expect(categorize('jina-reranker-v2')).toBe('jina');
    expect(categorize('mistral-large-latest')).toBe('mistral');
    expect(categorize('grok-3')).toBe('xai');
    expect(categorize('llama-3.3-70b')).toBe('llama');
    expect(categorize('doubao-pro-32k')).toBe('doubao');
    expect(categorize('yi-large')).toBe('yi');
  });

  it('matches the Gemini embedding prefix only at the start of the name', () => {
    expect(categorize('embedding-001')).toBe('gemini');
    // A name that merely contains "embedding-" must not be claimed by Gemini.
    expect(categorize('bge-embedding-zh')).toBeNull();
  });

  it('claims a Cloudflare-hosted Llama for Cloudflare, not for Llama', () => {
    // Both filters match '@cf/meta/llama-2'; cloudflare is declared first,
    // and first-match-wins is the documented resolution.
    expect(categorize('@cf/meta/llama-2-7b')).toBe('cloudflare');
  });

  it('returns null for a model no vendor filter recognises', () => {
    expect(categorize('acme-inhouse-v1')).toBeNull();
  });

  it.skip('should file 360 models under the 360 vendor', () => {
    // DEFECT: the `ai360` filter is `includes('360')` but it is declared
    // AFTER `openai`, whose filter includes `includes('gpt')`. Every real
    // 360 model name contains both ("360gpt-pro", "360GPT_S2_V9"), so the
    // ai360 category is unreachable for its own flagship models -- they get
    // the OpenAI icon in tables and the OpenAI chip in the pricing filter.
    expect(categorize('360gpt-pro')).toBe('ai360');
  });

  it('currently mis-files 360 models under OpenAI', () => {
    expect(categorize('360gpt-pro')).toBe('openai');
    // The 360 filter itself works; only the ordering defeats it.
    const cats = getModelCategories((k) => k);
    expect(cats.ai360.filter({ model_name: '360gpt-pro' })).toBe(true);
  });
});

describe('renderModelTag', () => {
  it('colours the tag from the model name and attaches a vendor icon', () => {
    render(<div>{renderModelTag('claude-3-5-sonnet')}</div>);
    const tag = screen.getByTestId('tag');
    expect(tag).toHaveTextContent('claude-3-5-sonnet');
    // Anthropic is a recognised vendor, so a prefix icon must be present.
    expect(tag.getAttribute('data-has-prefix')).toBe('yes');
    expect(tag.getAttribute('data-shape')).toBe('circle');
  });

  it('omits the icon for an unrecognised model', () => {
    render(<div>{renderModelTag('acme-inhouse-v1')}</div>);
    expect(screen.getByTestId('tag').getAttribute('data-has-prefix')).toBe(
      'no',
    );
  });

  it('lets the caller override colour, shape and suffix icon', () => {
    render(
      <div>
        {renderModelTag('gpt-4o', {
          color: 'red',
          shape: 'square',
          suffixIcon: <i />,
        })}
      </div>,
    );
    const tag = screen.getByTestId('tag');
    expect(tag.getAttribute('data-color')).toBe('red');
    expect(tag.getAttribute('data-shape')).toBe('square');
    expect(tag.getAttribute('data-has-suffix')).toBe('yes');
  });

  it('forwards the click handler', () => {
    const onClick = vi.fn();
    render(<div>{renderModelTag('gpt-4o', { onClick })}</div>);
    fireEvent.click(screen.getByTestId('tag'));
    expect(onClick).toHaveBeenCalledTimes(1);
  });
});

describe('renderGroup', () => {
  it('renders a single placeholder tag for the empty group', () => {
    render(<div>{renderGroup('')}</div>);
    const tags = screen.getAllByTestId('tag');
    expect(tags).toHaveLength(1);
    expect(tags[0]).toHaveTextContent('用户分组');
    expect(tags[0].getAttribute('data-color')).toBe('white');
  });

  it('splits on commas and sorts the group names', () => {
    render(<div>{renderGroup('zulu,alpha,mike')}</div>);
    const tags = screen.getAllByTestId('tag');
    expect(tags.map((t) => t.textContent)).toEqual(['alpha', 'mike', 'zulu']);
  });

  it('gives the premium tiers their reserved colours', () => {
    render(<div>{renderGroup('pro,svip,vip,premium')}</div>);
    const byName = Object.fromEntries(
      screen
        .getAllByTestId('tag')
        .map((t) => [t.textContent, t.getAttribute('data-color')]),
    );
    expect(byName.vip).toBe('yellow');
    expect(byName.pro).toBe('yellow');
    expect(byName.svip).toBe('red');
    expect(byName.premium).toBe('red');
  });

  it('derives a deterministic palette colour for an ordinary group', () => {
    render(<div>{renderGroup('alpha,beta')}</div>);
    const tags = screen.getAllByTestId('tag');
    // 'alpha' char codes sum to 518; 518 % 15 === 8 -> 'orange'
    expect(tags[0].getAttribute('data-color')).toBe('orange');
    // 'beta' sums to 412; 412 % 15 === 7 -> 'lime'
    expect(tags[1].getAttribute('data-color')).toBe('lime');
  });

  it('copies the group name and confirms when a tag is clicked', async () => {
    render(<div>{renderGroup('vip')}</div>);
    fireEvent.click(screen.getByTestId('tag'));
    await waitFor(() => expect(copy).toHaveBeenCalledWith('vip'));
    await waitFor(() =>
      expect(showSuccess).toHaveBeenCalledWith('已复制：vip'),
    );
    expect(Modal.error).not.toHaveBeenCalled();
  });

  it('offers a manual-copy dialog when the clipboard is unavailable', async () => {
    copy.mockResolvedValueOnce(false);
    render(<div>{renderGroup('vip')}</div>);
    fireEvent.click(screen.getByTestId('tag'));
    await waitFor(() => expect(Modal.error).toHaveBeenCalledTimes(1));
    expect(Modal.error.mock.calls[0][0].content).toBe('vip');
    expect(showSuccess).not.toHaveBeenCalled();
  });
});

describe('renderRatio colour thresholds', () => {
  const colorFor = (ratio) => {
    const { container } = render(<div>{renderRatio(ratio)}</div>);
    return container
      .querySelector('[data-testid="tag"]')
      .getAttribute('data-color');
  };

  it('is green at or below 1x', () => {
    expect(colorFor(0.5)).toBe('green');
    expect(colorFor(1)).toBe('green');
  });

  it('is blue above 1x and up to 3x', () => {
    expect(colorFor(1.5)).toBe('blue');
    expect(colorFor(3)).toBe('blue');
  });

  it('is orange above 3x and up to 5x', () => {
    expect(colorFor(3.5)).toBe('orange');
    expect(colorFor(5)).toBe('orange');
  });

  it('is red above 5x', () => {
    expect(colorFor(5.1)).toBe('red');
    expect(colorFor(20)).toBe('red');
  });

  it('labels the tag with the multiplier', () => {
    render(<div>{renderRatio(2.5)}</div>);
    expect(screen.getByTestId('tag')).toHaveTextContent('2.5x 倍率');
  });
});

describe('getChannelIcon', () => {
  it('maps a channel type to its vendor icon', () => {
    expect(getChannelIcon(1).type).toBe(OpenAI);
    // Azure OpenAI (3) shares the OpenAI mark.
    expect(getChannelIcon(3).type).toBe(OpenAI);
    expect(getChannelIcon(1).props.size).toBe(14);
  });

  it('returns null for unknown / custom channel types', () => {
    expect(getChannelIcon(9999)).toBeNull();
    expect(getChannelIcon(undefined)).toBeNull();
  });
});

describe('getLucideIcon', () => {
  it('maps a sidebar key to its icon component', () => {
    expect(getLucideIcon('token').type).toBe(Key);
  });

  it('falls back to the generic user icon for an unknown key', () => {
    expect(getLucideIcon('no-such-key').type).toBe(CircleUser);
  });

  it('uses currentColor when not selected and the theme colour when selected', () => {
    expect(getLucideIcon('token').props.color).toBe('currentColor');
    expect(getLucideIcon('token', true).props.color).toBe(
      'var(--semi-color-primary)',
    );
  });

  it('adds the scale-up class only for the selected item', () => {
    expect(getLucideIcon('token', true).props.className).toContain('scale-105');
    expect(getLucideIcon('token', false).props.className).not.toContain(
      'scale-105',
    );
  });

  it('aliases personal to the same icon as user', () => {
    expect(getLucideIcon('personal').type).toBe(getLucideIcon('user').type);
  });
});

describe('getLobeHubIcon', () => {
  it('renders a placeholder avatar when there is no icon name', () => {
    render(<div>{getLobeHubIcon('')}</div>);
    expect(screen.getByTestId('avatar')).toHaveTextContent('?');
  });

  it('trims whitespace before resolving', () => {
    expect(getLobeHubIcon('  OpenAI  ').type).toBe(OpenAI);
  });

  it('resolves a dotted sub-component such as Claude.Color', () => {
    expect(getLobeHubIcon('Claude.Color').type).toBe(Claude.Color);
  });

  it('applies the size argument when the descriptor does not set one', () => {
    expect(getLobeHubIcon('OpenAI', 28).props.size).toBe(28);
  });

  it('parses quoted, numeric, boolean and bare dotted props', () => {
    const el = getLobeHubIcon(
      "OpenAI.shape={'square'}.size=20.rounded=true.flat",
    );
    expect(el.props.shape).toBe('square');
    expect(el.props.size).toBe(20);
    expect(el.props.rounded).toBe(true);
    expect(el.props.flat).toBe(true);
  });

  it('lets an explicit size in the descriptor win over the argument', () => {
    expect(getLobeHubIcon('OpenAI.size=20', 99).props.size).toBe(20);
  });

  it('falls back to an initial-letter avatar for an unknown icon name', () => {
    render(<div>{getLobeHubIcon('NoSuchVendor')}</div>);
    expect(screen.getByTestId('avatar')).toHaveTextContent('N');
  });

  it.skip('should accept a fractional prop value', () => {
    // DEFECT: the descriptor is split on '.', so a decimal value is torn in
    // half before parseValue ever sees it. The value parser explicitly
    // supports decimals (/^-?\d+(?:\.\d+)?$/, render.jsx:450) which shows
    // they were intended, but that branch is unreachable through this API:
    // 'OpenAI.size={1.5}' yields size='{1' plus a junk prop named '5}'.
    expect(getLobeHubIcon('OpenAI.size={1.5}').props.size).toBe(1.5);
  });

  it('currently shreds a fractional prop value across two segments', () => {
    const el = getLobeHubIcon('OpenAI.size={1.5}');
    expect(el.props.size).toBe('{1');
    expect(el.props['5}']).toBe(true);
  });
});

describe('truncateText', () => {
  const LONG = 'a'.repeat(50);

  it('is a no-op on desktop regardless of width', () => {
    expect(truncateText(LONG, 10)).toBe(LONG);
  });

  describe('on a mobile viewport', () => {
    let widthSpy;

    beforeEach(() => {
      window.matchMedia = (query) => ({
        matches: true,
        media: query,
        addListener: () => {},
        removeListener: () => {},
        addEventListener: () => {},
        removeEventListener: () => {},
        dispatchEvent: () => false,
      });
      // jsdom reports 0 for every offsetWidth; make the measurement
      // proportional to length so the binary search has something to bisect.
      widthSpy = vi
        .spyOn(HTMLElement.prototype, 'offsetWidth', 'get')
        .mockImplementation(function () {
          return (this.textContent || '').length * 10;
        });
    });

    afterEach(() => {
      widthSpy.mockRestore();
      window.matchMedia = (query) => ({
        matches: false,
        media: query,
        addListener: () => {},
        removeListener: () => {},
        addEventListener: () => {},
        removeEventListener: () => {},
        dispatchEvent: () => false,
      });
    });

    it('returns short text untouched', () => {
      expect(truncateText('abc', 200)).toBe('abc');
    });

    it('passes falsy text straight back', () => {
      expect(truncateText('', 200)).toBe('');
      expect(truncateText(null, 200)).toBeNull();
    });

    it('binary-searches the longest prefix that fits the pixel budget', () => {
      // budget 200px at 10px/char = 20 chars including the 3-char ellipsis
      expect(truncateText(LONG, 200)).toBe('a'.repeat(17) + '...');
      expect(truncateText(LONG, 200)).toHaveLength(20);
    });

    it('resolves a percentage budget against the window width', () => {
      window.innerWidth = 500;
      // 50% of 500px = 250px = 25 chars incl. ellipsis
      expect(truncateText(LONG, '50%')).toBe('a'.repeat(22) + '...');
    });

    it('falls back to a character count when measurement throws', () => {
      const warn = vi.spyOn(console, 'warn').mockImplementation(() => {});
      const create = vi
        .spyOn(document, 'createElement')
        .mockImplementation(() => {
          throw new Error('no layout engine');
        });
      try {
        expect(truncateText(LONG, 200)).toBe('a'.repeat(17) + '...');
        expect(truncateText('short', 200)).toBe('short');
        expect(warn).toHaveBeenCalled();
      } finally {
        create.mockRestore();
        warn.mockRestore();
      }
    });
  });
});
