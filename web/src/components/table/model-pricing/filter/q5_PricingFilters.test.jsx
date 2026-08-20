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

/**
 * The five pricing filters plus the display-settings panel.
 *
 * Each of these is a pure translation from (models, allModels, current filter)
 * to the item list handed to SelectableButtonGroup. The interesting content is
 * therefore *the item list*: which options exist, what count sits on each one,
 * which are disabled, and what value the click reports back. The shared
 * SelectableButtonGroup is shimmed to a marker that publishes every one of
 * those as an attribute, so nothing here can pass merely by rendering.
 *
 * Counts in the fixtures are deliberately unequal (2 vs 1, never 1 vs 1) so a
 * hard-coded or off-by-one count cannot produce the same DOM as the real one.
 */

import React from 'react';
import { describe, it, expect, vi } from 'vitest';
import { render } from '@testing-library/react';

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

// Marker shim: every deciding prop is surfaced as an attribute and every item
// is a real button wired to onChange, so a click reports the exact value the
// production code put on the item (0 and '' included — they must survive).
vi.mock('../../../common/ui/SelectableButtonGroup', () => ({
  default: ({
    title,
    items,
    activeValue,
    onChange,
    withCheckbox,
    collapsible,
    loading,
  }) =>
    React.createElement(
      'div',
      {
        'data-testid': 'group',
        'data-title': String(title),
        'data-active': JSON.stringify(activeValue ?? null),
        'data-with-checkbox': withCheckbox ? 'yes' : 'no',
        'data-collapsible': collapsible === false ? 'no' : 'yes',
        'data-loading': String(!!loading),
      },
      (items || []).map((item) =>
        React.createElement(
          'button',
          {
            type: 'button',
            key: String(item.value),
            'data-testid': `item-${String(item.value)}`,
            'data-label': String(item.label),
            'data-tag-count':
              item.tagCount === undefined ? 'none' : String(item.tagCount),
            'data-disabled': item.disabled ? 'yes' : 'no',
            'data-has-icon': item.icon ? 'yes' : 'no',
            onClick: () => onChange(item.value),
          },
          String(item.label),
        ),
      ),
    ),
}));

vi.mock('../../../../helpers', () => ({
  getLobeHubIcon: (icon, size) =>
    React.createElement('i', {
      'data-testid': 'vendor-icon',
      'data-icon': String(icon),
      'data-size': String(size),
    }),
}));

import PricingQuotaTypes from './PricingQuotaTypes';
import PricingGroups from './PricingGroups';
import PricingEndpointTypes from './PricingEndpointTypes';
import PricingTags from './PricingTags';
import PricingVendors from './PricingVendors';
import PricingDisplaySettings from './PricingDisplaySettings';

const t = (k) => k;

const groups = (container) => [
  ...container.querySelectorAll('[data-testid="group"]'),
];
const itemsOf = (scope) =>
  [...scope.querySelectorAll('button')].map((b) => ({
    label: b.getAttribute('data-label'),
    count: b.getAttribute('data-tag-count'),
    disabled: b.getAttribute('data-disabled'),
    icon: b.getAttribute('data-has-icon'),
  }));

describe('PricingQuotaTypes', () => {
  // 2 per-token models against 1 per-call model: a swapped or hard-coded count
  // cannot reproduce this DOM.
  const models = [
    { model_name: 'a', quota_type: 0 },
    { model_name: 'b', quota_type: 0 },
    { model_name: 'c', quota_type: 1 },
  ];

  it('counts each billing type separately and totals them under 全部类型', () => {
    const { container } = render(
      <PricingQuotaTypes
        filterQuotaType='all'
        setFilterQuotaType={vi.fn()}
        models={models}
        t={t}
      />,
    );
    expect(itemsOf(container)).toEqual([
      { label: '全部类型', count: '3', disabled: 'no', icon: 'no' },
      { label: '按量计费', count: '2', disabled: 'no', icon: 'no' },
      { label: '按次计费', count: '1', disabled: 'no', icon: 'no' },
    ]);
  });

  // quota_type 0 is a real value, not "unset". Reporting it as a string, or
  // losing it to a `value || 'all'`, would silently show per-call prices.
  it('reports the numeric quota type 0 on click, not a string or a fallback', () => {
    const setFilterQuotaType = vi.fn();
    const { container } = render(
      <PricingQuotaTypes
        filterQuotaType='all'
        setFilterQuotaType={setFilterQuotaType}
        models={models}
        t={t}
      />,
    );
    container.querySelector('[data-testid="item-0"]').click();
    expect(setFilterQuotaType).toHaveBeenCalledWith(0);
    expect(setFilterQuotaType.mock.calls[0][0]).not.toBe('0');
  });

  it('marks the currently selected billing type as active', () => {
    const { container } = render(
      <PricingQuotaTypes
        filterQuotaType={1}
        setFilterQuotaType={vi.fn()}
        models={models}
        t={t}
      />,
    );
    expect(groups(container)[0]).toHaveAttribute('data-active', '1');
  });

  it('shows a zero count rather than hiding a type with no models', () => {
    const { container } = render(
      <PricingQuotaTypes
        filterQuotaType='all'
        setFilterQuotaType={vi.fn()}
        models={[{ model_name: 'a', quota_type: 0 }]}
        t={t}
      />,
    );
    const perCall = container.querySelector('[data-testid="item-1"]');
    expect(perCall).not.toBeNull();
    expect(perCall).toHaveAttribute('data-tag-count', '0');
  });
});

describe('PricingGroups', () => {
  const models = [
    { model_name: 'a', enable_groups: ['default', 'vip'] },
    { model_name: 'b', enable_groups: ['default'] },
    { model_name: 'c', enable_groups: ['default'] },
  ];
  const usableGroup = { '': {}, default: {}, vip: {}, svip: {} };

  it('lists 全部分组 plus every non-blank usable group', () => {
    const { container } = render(
      <PricingGroups
        filterGroup='all'
        setFilterGroup={vi.fn()}
        usableGroup={usableGroup}
        groupRatio={{ default: 1, vip: 0.5, svip: 0.25 }}
        models={models}
        t={t}
      />,
    );
    expect(itemsOf(container).map((i) => i.label)).toEqual([
      '全部分组',
      'default',
      'vip',
      'svip',
    ]);
  });

  // A group ratio of 0 means "free for this group". `ratio || 1` would print
  // x1 and quietly bill the customer at full price on a free tier, so the
  // zero has to survive to the label. This is the same falsy-zero hazard the
  // sibling ModelPricingTable gets wrong (see q5_ModelPricingTable.test.jsx).
  it('prints a zero group ratio as x0, never as x1', () => {
    const { container } = render(
      <PricingGroups
        filterGroup='all'
        setFilterGroup={vi.fn()}
        usableGroup={{ free: {} }}
        groupRatio={{ free: 0 }}
        models={[{ model_name: 'a', enable_groups: ['free'] }]}
        t={t}
      />,
    );
    expect(
      container.querySelector('[data-testid="item-free"]'),
    ).toHaveAttribute('data-tag-count', 'x0');
  });

  it('falls back to x1 only when the group has no ratio configured at all', () => {
    const { container } = render(
      <PricingGroups
        filterGroup='all'
        setFilterGroup={vi.fn()}
        usableGroup={{ ghost: {} }}
        groupRatio={{}}
        models={[{ model_name: 'a', enable_groups: ['ghost'] }]}
        t={t}
      />,
    );
    expect(
      container.querySelector('[data-testid="item-ghost"]'),
    ).toHaveAttribute('data-tag-count', 'x1');
  });

  // Positive/negative pair: a group nobody can use is disabled, a group with
  // models is not. Without the negative half "always disabled" would pass.
  it('disables a group no model is enabled for and leaves populated ones alive', () => {
    const { container } = render(
      <PricingGroups
        filterGroup='all'
        setFilterGroup={vi.fn()}
        usableGroup={usableGroup}
        groupRatio={{ default: 1, vip: 0.5, svip: 0.25 }}
        models={models}
        t={t}
      />,
    );
    expect(
      container.querySelector('[data-testid="item-svip"]'),
    ).toHaveAttribute('data-disabled', 'yes');
    expect(container.querySelector('[data-testid="item-vip"]')).toHaveAttribute(
      'data-disabled',
      'no',
    );
  });

  it('routes the click through handleGroupClick with the group name', () => {
    const setFilterGroup = vi.fn();
    const { container } = render(
      <PricingGroups
        filterGroup='all'
        setFilterGroup={setFilterGroup}
        usableGroup={usableGroup}
        groupRatio={{ default: 1, vip: 0.5, svip: 0.25 }}
        models={models}
        t={t}
      />,
    );
    container.querySelector('[data-testid="item-vip"]').click();
    expect(setFilterGroup).toHaveBeenCalledWith('vip');
  });
});

describe('PricingEndpointTypes', () => {
  // allModels holds the full option universe; models is the already-filtered
  // slice used for counts. Keeping them different is what proves the component
  // reads the right list for each job.
  const allModels = [
    { model_name: 'a', supported_endpoint_types: ['/v1/chat/completions'] },
    { model_name: 'b', supported_endpoint_types: ['/v1/chat/completions'] },
    { model_name: 'c', supported_endpoint_types: ['/v1/embeddings'] },
    { model_name: 'd' },
  ];
  const models = allModels.slice(0, 2);

  it('offers every endpoint in the catalogue, sorted, counted from the filtered slice', () => {
    const { container } = render(
      <PricingEndpointTypes
        filterEndpointType='all'
        setFilterEndpointType={vi.fn()}
        models={models}
        allModels={allModels}
        t={t}
      />,
    );
    expect(itemsOf(container)).toEqual([
      { label: '全部端点', count: '2', disabled: 'no', icon: 'no' },
      {
        label: '/v1/chat/completions',
        count: '2',
        disabled: 'no',
        icon: 'no',
      },
      { label: '/v1/embeddings', count: '0', disabled: 'yes', icon: 'no' },
    ]);
  });

  it('derives the option list from models when no catalogue is supplied', () => {
    const { container } = render(
      <PricingEndpointTypes
        filterEndpointType='all'
        setFilterEndpointType={vi.fn()}
        models={models}
        t={t}
      />,
    );
    expect(itemsOf(container).map((i) => i.label)).toEqual([
      '全部端点',
      '/v1/chat/completions',
    ]);
  });

  it('disables 全部端点 when nothing survived the other filters', () => {
    const { container } = render(
      <PricingEndpointTypes
        filterEndpointType='all'
        setFilterEndpointType={vi.fn()}
        models={[]}
        allModels={allModels}
        t={t}
      />,
    );
    expect(container.querySelector('[data-testid="item-all"]')).toHaveAttribute(
      'data-disabled',
      'yes',
    );
  });

  it('reports the endpoint string on click', () => {
    const setFilterEndpointType = vi.fn();
    const { container } = render(
      <PricingEndpointTypes
        filterEndpointType='all'
        setFilterEndpointType={setFilterEndpointType}
        models={models}
        allModels={allModels}
        t={t}
      />,
    );
    container
      .querySelector('[data-testid="item-/v1/chat/completions"]')
      .click();
    expect(setFilterEndpointType).toHaveBeenCalledWith('/v1/chat/completions');
  });
});

describe('PricingTags', () => {
  const allModels = [
    { model_name: 'a', tags: 'Vision, Tools' },
    { model_name: 'b', tags: 'vision|open weights' },
    { model_name: 'c', tags: 'reasoning' },
    { model_name: 'd' },
  ];
  const models = allModels.slice(0, 2);

  it('splits on comma, semicolon and pipe, lower-cases and sorts the catalogue', () => {
    const { container } = render(
      <PricingTags
        filterTag='all'
        setFilterTag={vi.fn()}
        models={models}
        allModels={allModels}
        t={t}
      />,
    );
    expect(itemsOf(container).map((i) => i.label)).toEqual([
      '全部标签',
      'open weights', // multi-word tag survives: the split keeps inner spaces
      'reasoning',
      'tools',
      'vision',
    ]);
  });

  it('counts case-insensitively across both spellings of a tag', () => {
    const { container } = render(
      <PricingTags
        filterTag='all'
        setFilterTag={vi.fn()}
        models={models}
        allModels={allModels}
        t={t}
      />,
    );
    // 'Vision' + 'vision' = 2; 'Tools' alone = 1 — unequal on purpose.
    expect(
      container.querySelector('[data-testid="item-vision"]'),
    ).toHaveAttribute('data-tag-count', '2');
    expect(
      container.querySelector('[data-testid="item-tools"]'),
    ).toHaveAttribute('data-tag-count', '1');
    expect(
      container.querySelector('[data-testid="item-reasoning"]'),
    ).toHaveAttribute('data-tag-count', '0');
  });

  it('disables a tag no visible model carries', () => {
    const { container } = render(
      <PricingTags
        filterTag='all'
        setFilterTag={vi.fn()}
        models={models}
        allModels={allModels}
        t={t}
      />,
    );
    expect(
      container.querySelector('[data-testid="item-reasoning"]'),
    ).toHaveAttribute('data-disabled', 'yes');
    expect(
      container.querySelector('[data-testid="item-vision"]'),
    ).toHaveAttribute('data-disabled', 'no');
  });

  it('reports the lower-cased tag on click', () => {
    const setFilterTag = vi.fn();
    const { container } = render(
      <PricingTags
        filterTag='all'
        setFilterTag={setFilterTag}
        models={models}
        allModels={allModels}
        t={t}
      />,
    );
    container.querySelector('[data-testid="item-vision"]').click();
    expect(setFilterTag).toHaveBeenCalledWith('vision');
  });
});

describe('PricingVendors', () => {
  const allModels = [
    { model_name: 'a', vendor_name: 'OpenAI', vendor_icon: 'OpenAI' },
    { model_name: 'b', vendor_name: 'OpenAI' },
    { model_name: 'c', vendor_name: 'Anthropic' },
    { model_name: 'd' }, // no vendor at all
  ];

  it('sorts known vendors and appends 未知供应商 only when one exists', () => {
    const { container } = render(
      <PricingVendors
        filterVendor='all'
        setFilterVendor={vi.fn()}
        models={allModels}
        allModels={allModels}
        t={t}
      />,
    );
    expect(itemsOf(container).map((i) => i.label)).toEqual([
      '全部供应商',
      'Anthropic',
      'OpenAI',
      '未知供应商',
    ]);
  });

  // Negative half: with every model attributed there must be no unknown bucket.
  it('omits 未知供应商 when every model is attributed', () => {
    const attributed = allModels.slice(0, 3);
    const { container } = render(
      <PricingVendors
        filterVendor='all'
        setFilterVendor={vi.fn()}
        models={attributed}
        allModels={attributed}
        t={t}
      />,
    );
    expect(itemsOf(container).map((i) => i.label)).not.toContain('未知供应商');
  });

  it('counts per vendor from the filtered slice and disables empty ones', () => {
    const { container } = render(
      <PricingVendors
        filterVendor='all'
        setFilterVendor={vi.fn()}
        models={allModels.filter((m) => m.vendor_name === 'OpenAI')}
        allModels={allModels}
        t={t}
      />,
    );
    expect(
      container.querySelector('[data-testid="item-OpenAI"]'),
    ).toHaveAttribute('data-tag-count', '2');
    expect(
      container.querySelector('[data-testid="item-Anthropic"]'),
    ).toHaveAttribute('data-tag-count', '0');
    expect(
      container.querySelector('[data-testid="item-Anthropic"]'),
    ).toHaveAttribute('data-disabled', 'yes');
  });

  // The icon is taken from the first model that declares one — vendor 'OpenAI'
  // has an icon on model a but not on model b, and it must still show.
  it('attaches an icon to a vendor that declares one and none to a vendor that does not', () => {
    const { container } = render(
      <PricingVendors
        filterVendor='all'
        setFilterVendor={vi.fn()}
        models={allModels}
        allModels={allModels}
        t={t}
      />,
    );
    expect(
      container.querySelector('[data-testid="item-OpenAI"]'),
    ).toHaveAttribute('data-has-icon', 'yes');
    expect(
      container.querySelector('[data-testid="item-Anthropic"]'),
    ).toHaveAttribute('data-has-icon', 'no');
  });

  it('reports the sentinel "unknown" for the unattributed bucket', () => {
    const setFilterVendor = vi.fn();
    const { container } = render(
      <PricingVendors
        filterVendor='all'
        setFilterVendor={setFilterVendor}
        models={allModels}
        allModels={allModels}
        t={t}
      />,
    );
    container.querySelector('[data-testid="item-unknown"]').click();
    expect(setFilterVendor).toHaveBeenCalledWith('unknown');
  });
});

describe('PricingDisplaySettings', () => {
  const baseProps = (over = {}) => ({
    showWithRecharge: false,
    setShowWithRecharge: vi.fn(),
    currency: 'USD',
    setCurrency: vi.fn(),
    showRatio: false,
    setShowRatio: vi.fn(),
    viewMode: 'card',
    setViewMode: vi.fn(),
    tokenUnit: 'M',
    setTokenUnit: vi.fn(),
    t,
    ...over,
  });

  it('derives the active set from the four independent settings', () => {
    const { container } = render(
      <PricingDisplaySettings
        {...baseProps({
          showWithRecharge: true,
          showRatio: false,
          viewMode: 'table',
          tokenUnit: 'M',
        })}
      />,
    );
    expect(
      JSON.parse(groups(container)[0].getAttribute('data-active')),
    ).toEqual(['recharge', 'tableView']);
  });

  it('leaves the active set empty when every setting is off', () => {
    const { container } = render(<PricingDisplaySettings {...baseProps()} />);
    expect(
      JSON.parse(groups(container)[0].getAttribute('data-active')),
    ).toEqual([]);
  });

  // Each toggle must flip its own switch and nothing else: a copy-paste slip
  // in the switch statement would move the wrong setting.
  it('toggles only the setting that was clicked', () => {
    const props = baseProps({ showRatio: false });
    const { container } = render(<PricingDisplaySettings {...props} />);
    container.querySelector('[data-testid="item-ratio"]').click();
    expect(props.setShowRatio).toHaveBeenCalledWith(true);
    expect(props.setShowWithRecharge).not.toHaveBeenCalled();
    expect(props.setViewMode).not.toHaveBeenCalled();
    expect(props.setTokenUnit).not.toHaveBeenCalled();
  });

  it('flips the view mode both ways rather than always setting table', () => {
    const asCard = baseProps({ viewMode: 'card' });
    const { container: c1 } = render(<PricingDisplaySettings {...asCard} />);
    c1.querySelector('[data-testid="item-tableView"]').click();
    expect(asCard.setViewMode).toHaveBeenCalledWith('table');

    const asTable = baseProps({ viewMode: 'table' });
    const { container: c2 } = render(<PricingDisplaySettings {...asTable} />);
    c2.querySelector('[data-testid="item-tableView"]').click();
    expect(asTable.setViewMode).toHaveBeenCalledWith('card');
  });

  // The token unit divides the quoted price by 1000, so a stuck toggle
  // misquotes by three orders of magnitude.
  it('flips the token unit both ways', () => {
    const asM = baseProps({ tokenUnit: 'M' });
    const { container: c1 } = render(<PricingDisplaySettings {...asM} />);
    c1.querySelector('[data-testid="item-tokenUnit"]').click();
    expect(asM.setTokenUnit).toHaveBeenCalledWith('K');

    const asK = baseProps({ tokenUnit: 'K' });
    const { container: c2 } = render(<PricingDisplaySettings {...asK} />);
    c2.querySelector('[data-testid="item-tokenUnit"]').click();
    expect(asK.setTokenUnit).toHaveBeenCalledWith('M');
  });

  it('reveals the currency picker only while recharge pricing is on', () => {
    const { container: off } = render(
      <PricingDisplaySettings {...baseProps()} />,
    );
    expect(groups(off)).toHaveLength(1);

    const { container: on } = render(
      <PricingDisplaySettings {...baseProps({ showWithRecharge: true })} />,
    );
    expect(groups(on)).toHaveLength(2);
    expect(groups(on)[1]).toHaveAttribute('data-title', '货币单位');
    expect(itemsOf(groups(on)[1]).map((i) => i.label)).toEqual([
      'USD ($)',
      'CNY (¥)',
      '自定义货币',
    ]);
  });

  it('sets the currency directly from the picker', () => {
    const props = baseProps({ showWithRecharge: true });
    const { container } = render(<PricingDisplaySettings {...props} />);
    container.querySelector('[data-testid="item-CNY"]').click();
    expect(props.setCurrency).toHaveBeenCalledWith('CNY');
    // The currency picker must not also toggle a display switch.
    expect(props.setShowRatio).not.toHaveBeenCalled();
  });

  it('marks the active currency and renders the settings group with checkboxes', () => {
    const { container } = render(
      <PricingDisplaySettings
        {...baseProps({ showWithRecharge: true, currency: 'CNY' })}
      />,
    );
    expect(groups(container)[0]).toHaveAttribute('data-with-checkbox', 'yes');
    expect(groups(container)[1]).toHaveAttribute('data-active', '"CNY"');
  });

  it('passes the loading flag down to both groups', () => {
    const { container } = render(
      <PricingDisplaySettings
        {...baseProps({ showWithRecharge: true, loading: true })}
      />,
    );
    expect(
      groups(container).map((g) => g.getAttribute('data-loading')),
    ).toEqual(['true', 'true']);
  });
});
