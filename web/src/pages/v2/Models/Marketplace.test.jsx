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

// Vendor logos come from a ~4 MB pack loaded on first render; nothing here
// depends on it (same stub as Models/index.test.jsx).
vi.mock('@lobehub/icons', () => ({}));

import Marketplace from './Marketplace';

// Mirror i18next's en behaviour: return the English defaultValue (2nd arg)
// with {{var}} interpolation, falling back to the key when no default given.
const tr = (key, fallback, opts) => {
  const vars =
    typeof fallback === 'object' && fallback !== null ? fallback : opts;
  let out = typeof fallback === 'string' ? fallback : key;
  if (vars) {
    for (const [k, v] of Object.entries(vars)) {
      out = out.split('{{' + k + '}}').join(String(v));
    }
  }
  return out;
};

const noop = () => {};

// A buildCatalog()-shaped entry with sane defaults — every field the
// component reads is present, so a test only has to override what it cares
// about.
const mkEntry = (id, overrides = {}) => ({
  id,
  vendor: 'Vendor',
  description: '',
  tags: [],
  capabilities: [],
  routable: true,
  priced: true,
  quotaType: 0,
  inputPerM: 0.27,
  outputPerM: 1.08,
  cacheReadPerM: null,
  perCall: null,
  tokens: 0,
  requests: 0,
  catalogueId: null,
  status: null,
  p50Ms: null,
  p95Ms: null,
  errorRate: null,
  enoughSamples: false,
  ...overrides,
});

const renderMarketplace = (entries, props = {}) =>
  render(
    <Marketplace
      entries={entries}
      loading={false}
      onTry={noop}
      onKeys={noop}
      onCopy={noop}
      base='https://hub.example.test'
      tr={tr}
      {...props}
    />,
  );

beforeEach(() => {
  // A clean, param-free URL by default — the small-catalog collapse (below)
  // must not see a stray filter param left by a previous test.
  window.history.pushState({}, '', '/console/v2/models');
});

describe('Marketplace price pills', () => {
  it('renders in/out pills with /M for a ratio-priced model', () => {
    renderMarketplace([mkEntry('m-ratio')]);
    const card = screen.getByTestId('model-card-m-ratio');
    expect(card.textContent).toContain('$0.27');
    expect(card.textContent).toContain('/M input');
    expect(card.textContent).toContain('$1.08');
    expect(card.textContent).toContain('/M output');
  });

  it('renders only a per-call pill for a pay-per-call model', () => {
    renderMarketplace([
      mkEntry('m-call', {
        quotaType: 1,
        perCall: 0.1,
        inputPerM: null,
        outputPerM: null,
      }),
    ]);
    const card = screen.getByTestId('model-card-m-call');
    expect(card.textContent).toContain('$0.1');
    expect(card.textContent).toContain('per call');
    expect(card.textContent).not.toContain('/M input');
  });

  it('renders a no-traffic placeholder — as an element, not a gap — when the card has no performance data', () => {
    renderMarketplace([mkEntry('m-quiet')]);
    const perf = screen.getByTestId('model-perf-m-quiet');
    expect(perf.textContent).toMatch(/no traffic yet/);
  });
});

describe('Marketplace small-catalog collapse', () => {
  it('hides the facet rail and sort control for an unfiltered catalog under the threshold', () => {
    renderMarketplace([mkEntry('m-0'), mkEntry('m-1'), mkEntry('m-2')]);
    expect(screen.queryByTestId('models-rail')).toBeNull();
    expect(screen.queryByTestId('models-sort')).toBeNull();
    expect(screen.queryByTestId('models-view-table')).toBeNull();
  });

  it('shows the full chrome once the catalog reaches the threshold', () => {
    const entries = Array.from({ length: 6 }, (_, i) => mkEntry(`m-${i}`));
    renderMarketplace(entries);
    expect(screen.getByTestId('models-rail')).toBeInTheDocument();
    expect(screen.getByTestId('models-sort')).toBeInTheDocument();
  });

  it('does not collapse a small catalog whose URL already carries a filter param', () => {
    window.history.pushState({}, '', '/console/v2/models?vendor=OpenAI');
    renderMarketplace([mkEntry('m-0'), mkEntry('m-1'), mkEntry('m-2')]);
    expect(screen.getByTestId('models-rail')).toBeInTheDocument();
  });
});
