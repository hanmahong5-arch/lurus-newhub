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

/*
 * The i18n gate proves every string on this page goes through t() and that
 * every key resolves in en.json. Neither proves the page RENDERS in English —
 * a label could be wired to a key nobody reaches, or a status could fall
 * through a lookup table. This mounts the real page against the real bundle
 * with the language set to English and asserts there is no Chinese on screen,
 * which is what an operator was actually shown when this was reported: the
 * heading, every table column and every form field in Chinese.
 */
import React from 'react';
import { render, screen, waitFor, act } from '@testing-library/react';
import { describe, it, expect, beforeAll, beforeEach, vi } from 'vitest';

// Semi UI pulls in lottie-web, which paints into a canvas the moment it is
// imported; jsdom has no 2D context and the whole module graph dies before a
// single test runs. Every other suite works around it by stubbing Semi itself,
// but a stub cannot tell us what the real components render. vi.hoisted runs
// before the imports below, which is early enough for lottie's own top-level
// code to find a context.
vi.hoisted(() => {
  const ctx = new Proxy(
    {},
    {
      get: (target, prop) =>
        prop in target ? target[prop] : () => ({ data: [] }),
      set: (target, prop, value) => ((target[prop] = value), true),
    },
  );
  HTMLCanvasElement.prototype.getContext = () => ctx;
});

vi.mock('../../helpers/api', () => ({
  API: { get: vi.fn(), post: vi.fn(), put: vi.fn(), delete: vi.fn() },
}));

import { API } from '../../helpers/api';
import i18n from '../../i18n/i18n';
import OpenRouterSync from './index';

const HAN = /[一-鿿]/;

const JOB = {
  id: 7,
  name: 'nightly-free',
  target_channel_id: 3,
  categories: '["reasoning"]',
  top_n: 0, // renders the "unlimited" branch
  schedule: 'daily',
  enabled: false, // renders the "no" branch
  last_run_at: null, // renders the em-dash branch
};

const POOL = [
  {
    channel_id: 3,
    channel_name: 'or-main',
    status: 'auto_disabled',
    enabled_count: 1,
    cooling_count: 1,
    permanent_disabled_count: 1,
    key_count: 3,
    keys: [
      { index: 0, key_prefix: 'sk-a', status: 'enabled' },
      {
        index: 1,
        key_prefix: 'sk-b',
        status: 'cooling',
        cooldown_seconds_remaining: 0, // renders "recovering shortly"
      },
      { index: 2, key_prefix: 'sk-c', status: 'permanent_disabled' },
    ],
  },
];

const ok = (data) => ({ data: { success: true, data } });

beforeAll(async () => {
  await i18n.changeLanguage('en');
});

beforeEach(() => {
  vi.clearAllMocks();
  API.get.mockImplementation((url) => {
    if (url.startsWith('/api/openrouter-sync/api-pool')) return ok(POOL);
    if (url.startsWith('/api/openrouter-sync/jobs')) return ok([JOB]);
    if (url.startsWith('/api/openrouter-sync/categories'))
      return ok([{ key: 'reasoning', label: 'Reasoning' }]);
    if (url.startsWith('/api/channel/'))
      return ok({ items: [{ id: 3, name: 'or-main', type: 20 }] });
    return ok([]);
  });
});

const chinese = (text) =>
  [...text].filter((c) => HAN.test(c)).length
    ? text
        .split(/(?=[一-鿿])|(?<=[一-鿿])/)
        .filter((s) => HAN.test(s))
        .join('')
    : '';

describe('/console/openrouter-sync in English', () => {
  it('renders the list, the pool card and the columns without Chinese', async () => {
    const { container } = render(<OpenRouterSync />);

    await waitFor(() => expect(screen.getByText('or-main')).toBeTruthy());
    // The pool card polls; let its first paint settle before reading.
    await waitFor(() => expect(screen.getByText('sk-a')).toBeTruthy());

    const text = container.textContent || '';
    expect(text).toContain('OpenRouter free-model sync');
    expect(text).toContain('Last run');
    expect(text).toContain('Unlimited');
    expect(text).toContain('Auto-disabled (pending recovery)');
    expect(text).toContain('Recovering shortly');
    expect(
      chinese(text),
      `Chinese rendered on /console/openrouter-sync under i18n.language=en`,
    ).toBe('');
  });

  it('renders the job form without Chinese', async () => {
    render(<OpenRouterSync />);
    await waitFor(() => expect(screen.getByText('New job')).toBeTruthy());

    await act(async () => {
      screen.getByText('New job').closest('button').click();
    });
    await waitFor(() => expect(screen.getByText('Job name')).toBeTruthy());

    // The modal is portalled out of the container, so read the document.
    const text = document.body.textContent || '';
    expect(text).toContain('Job name');
    expect(text).toContain('Top N (0 = unlimited)');
    expect(
      chinese(text),
      `Chinese rendered in the job form under i18n.language=en`,
    ).toBe('');
  });
});
