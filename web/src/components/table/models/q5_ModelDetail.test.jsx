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
 * The model detail drawer and its three panes.
 *
 * ModelDetailPricing is the money surface: it is where an operator confirms
 * that a model is priced, and how. The drawer itself owns the destructive
 * actions (disable / delete), so both are exercised against a record whose id
 * (7) differs from its position, and the delete path is checked for the
 * property that actually protects the catalogue — the first click confirms,
 * it does not delete.
 *
 * The three panes are rendered for real inside the drawer rather than stubbed,
 * because "the drawer handed the right model to the right pane" is the only
 * thing worth asserting about the wiring.
 */

import React from 'react';
import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, fireEvent, waitFor } from '@testing-library/react';

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

const semiModalConfirm = vi.hoisted(() => vi.fn());

vi.mock('@douyinfe/semi-ui', () => {
  const Tabs = ({ children, activeKey, onChange }) => {
    const panes = React.Children.toArray(children).filter(Boolean);
    return React.createElement(
      'div',
      { 'data-testid': 'tabs', 'data-active-key': String(activeKey) },
      panes.map((p, i) =>
        React.createElement(
          'button',
          {
            key: `tab-${i}`,
            type: 'button',
            'data-testid': `tab-${p.props.itemKey}`,
            onClick: () => onChange?.(p.props.itemKey),
          },
          p.props.tab,
        ),
      ),
      panes
        .filter((p) => String(p.props.itemKey) === String(activeKey))
        .map((p, i) =>
          React.createElement(
            'div',
            { key: `pane-${i}`, 'data-testid': `pane-${p.props.itemKey}` },
            p.props.children,
          ),
        ),
    );
  };
  Tabs.TabPane = ({ children }) => children;

  return {
    Tabs,
    TabPane: Tabs.TabPane,
    SideSheet: ({ title, visible, children, onCancel }) =>
      React.createElement(
        'div',
        { 'data-testid': 'side-sheet', 'data-visible': String(!!visible) },
        React.createElement('div', { 'data-testid': 'sheet-title' }, title),
        React.createElement('button', {
          type: 'button',
          'data-testid': 'sheet-cancel',
          onClick: onCancel,
        }),
        children,
      ),
    Button: ({ children, onClick, type }) =>
      React.createElement(
        'button',
        {
          type: 'button',
          'data-testid': `btn-${String(children)}`,
          'data-type': type,
          onClick,
        },
        children,
      ),
    Space: ({ children }) => React.createElement('div', null, children),
    Modal: { confirm: semiModalConfirm },
    Tag: ({ children, color, prefixIcon }) =>
      React.createElement(
        'span',
        { 'data-testid': 'tag', 'data-color': color },
        prefixIcon,
        children,
      ),
    Card: ({ children }) =>
      React.createElement('div', { 'data-testid': 'card' }, children),
    Typography: {
      Text: ({ children, copyable, type }) =>
        React.createElement(
          'span',
          {
            'data-testid': 'text',
            'data-copyable': copyable ? 'yes' : 'no',
            'data-type': type,
          },
          children,
        ),
      Title: ({ children }) =>
        React.createElement('h6', { 'data-testid': 'title' }, children),
    },
  };
});

vi.mock('react-i18next', () => ({
  useTranslation: () => ({ t: (k) => k }),
}));

vi.mock('../../../hooks/common/useIsMobile', () => ({
  MOBILE_BREAKPOINT: 768,
  useIsMobile: () => false,
}));

vi.mock('../../../helpers/render', () => ({
  getLobeHubIcon: (icon, size) =>
    React.createElement('i', {
      'data-testid': 'header-icon',
      'data-icon': String(icon),
      'data-size': String(size),
    }),
}));

vi.mock('../../../helpers', () => ({
  getLobeHubIcon: (icon, size) =>
    React.createElement('i', {
      'data-testid': 'lobe-icon',
      'data-icon': String(icon),
      'data-size': String(size),
    }),
  stringToColor: (s) => `color-${s}`,
  timestamp2string: (ts) => `ts(${ts})`,
}));

vi.mock('lucide-react', () => ({
  AlertTriangle: () => React.createElement('i', { 'data-testid': 'warn-icon' }),
  DollarSign: () => React.createElement('i', { 'data-testid': 'dollar-icon' }),
  GitBranch: () => React.createElement('i', { 'data-testid': 'branch-icon' }),
  ExternalLink: () => React.createElement('i', { 'data-testid': 'link-icon' }),
}));

vi.mock('@douyinfe/semi-illustrations', () => ({
  IllustrationNoContent: () =>
    React.createElement('i', { 'data-testid': 'no-content' }),
  IllustrationNoContentDark: () =>
    React.createElement('i', { 'data-testid': 'no-content-dark' }),
}));

import ModelDetailDrawer from './ModelDetailDrawer';
import ModelDetailPricing from './ModelDetailPricing';
import ModelDetailChannels from './ModelDetailChannels';
import ModelDetailOverview from './ModelDetailOverview';

const t = (k) => k;

const vendorMap = { 10: { name: 'OpenAI', icon: 'OpenAI' } };

const model = (over = {}) => ({
  id: 7,
  model_name: 'gpt-4o',
  status: 1,
  name_rule: 1,
  sync_official: 1,
  vendor_id: 10,
  ...over,
});

beforeEach(() => {
  semiModalConfirm.mockReset();
});

describe('ModelDetailPricing — is this model priced', () => {
  const renderPricing = (pricingMap, over = {}) =>
    render(
      <ModelDetailPricing
        model={model()}
        pricingMap={pricingMap}
        t={t}
        {...over}
      />,
    );

  it('renders nothing at all without a model', () => {
    const { container } = render(
      <ModelDetailPricing model={null} pricingMap={{}} t={t} />,
    );
    expect(container).toBeEmptyDOMElement();
  });

  it('warns loudly when the model is absent from the pricing map', () => {
    const { container } = renderPricing({});
    expect(container.textContent).toContain('未定价');
    expect(container.textContent).toContain('未配置倍率，调用将失败');
    expect(container.querySelector('[data-testid="tag"]')).toHaveAttribute(
      'data-color',
      'red',
    );
    expect(container.querySelector('[data-testid="warn-icon"]')).not.toBeNull();
  });

  it('shows an explicit ratio to three decimals under a green badge', () => {
    const { container } = renderPricing({
      'gpt-4o': { source: 'explicit', ratio: 2.5 },
    });
    expect(container.textContent).toContain('2.500');
    expect(container.querySelector('[data-testid="tag"]')).toHaveAttribute(
      'data-color',
      'green',
    );
    expect(container.textContent).toContain('显式配置');
    expect(container.textContent).not.toContain('未配置倍率，调用将失败');
  });

  it('shows a deliberately free model as 0.000 rather than as unpriced', () => {
    const { container } = renderPricing({
      'gpt-4o': { source: 'explicit', ratio: 0 },
    });
    expect(container.textContent).toContain('0.000');
    expect(container.textContent).not.toContain('未定价');
  });

  // ratio 2.2 with markup 1.1 gives base 2.000 — three values that are all
  // different, so a mix-up between base, markup and final cannot pass.
  it('decomposes an inferred ratio into family, base, markup and final', () => {
    const { container } = renderPricing({
      'gpt-4o': {
        source: 'family_fallback',
        ratio: 2.2,
        family: 'gpt-4',
        markup: 1.1,
      },
    });
    expect(container.textContent).toContain('gpt-4');
    expect(container.textContent).toContain('2.000'); // base = ratio / markup
    expect(container.textContent).toContain('×1.1'); // markup
    expect(container.textContent).toContain('≈ 2.200'); // final
    expect(container.querySelector('[data-testid="tag"]')).toHaveAttribute(
      'data-color',
      'blue',
    );
  });

  it('falls back to the final ratio as the base when no markup is recorded', () => {
    const { container } = renderPricing({
      'gpt-4o': { source: 'family_fallback', ratio: 2.2, family: 'gpt-4' },
    });
    expect(container.textContent).toContain('2.200');
    expect(container.textContent).toContain('-'); // markup cell
    expect(container.textContent).not.toContain('×');
  });

  // ------------------------------------------------------------------
  // DEFECT (operational): ModelDetailPricing.jsx:31-33 picks the badge colour
  // through a lookup that falls back to the 'none' config, but the explanatory
  // body is gated on `source === 'none'` by strict equality. A source the
  // frontend does not recognise — a new backend value, a typo, a partially
  // migrated row — therefore renders the red 未定价 badge over an EMPTY card:
  // the operator is told something is wrong and given no statement of what,
  // and no instruction on how to fix it. The three known sources all render a
  // body, so the unknown case is the only silent one.
  // Fix direction: treat "not explicit and not family_fallback" as the
  // unpriced case, exactly as ModelsColumnDefs.jsx:207 already does.
  // ------------------------------------------------------------------
  it.skip('CONTRACT: an unrecognised pricing source explains itself like none does', () => {
    const { container } = renderPricing({
      'gpt-4o': { source: 'brand_new_source', ratio: 1 },
    });
    expect(container.textContent).toContain('未配置倍率，调用将失败');
  });

  // Live invariant, true before and after that repair: an unrecognised source
  // must never be presented as priced.
  it('never presents an unrecognised pricing source as priced', () => {
    const { container } = renderPricing({
      'gpt-4o': { source: 'brand_new_source', ratio: 1 },
    });
    expect(container.textContent).toContain('未定价');
    expect(container.textContent).not.toContain('显式配置');
    expect(container.textContent).not.toContain('自动定价');
    expect(container.querySelector('[data-testid="tag"]')).toHaveAttribute(
      'data-color',
      'red',
    );
  });
});

describe('ModelDetailChannels', () => {
  it('shows the empty illustration when nothing is bound', () => {
    const { container } = render(<ModelDetailChannels model={model()} t={t} />);
    expect(container.textContent).toContain('暂无绑定渠道');
    expect(
      container.querySelector('[data-testid="no-content"]'),
    ).not.toBeNull();
  });

  it('treats a model with an empty channel list the same as none', () => {
    const { container } = render(
      <ModelDetailChannels model={model({ bound_channels: [] })} t={t} />,
    );
    expect(container.textContent).toContain('暂无绑定渠道');
  });

  it('counts and names every bound channel with its type', () => {
    const { container } = render(
      <ModelDetailChannels
        model={model({
          bound_channels: [
            { name: 'azure-eu', type: 3 },
            { name: 'openai-main', type: 1 },
          ],
        })}
        t={t}
      />,
    );
    expect(container.textContent).toContain('已绑定 2 个渠道');
    expect(container.textContent).toContain('azure-eu');
    expect(container.textContent).toContain('openai-main');
    expect(
      [...container.querySelectorAll('[data-testid="tag"]')].map(
        (n) => n.textContent,
      ),
    ).toEqual(['3', '1']);
    expect(container.querySelectorAll('a')).toHaveLength(2);
    expect(container.querySelector('a')).toHaveAttribute(
      'href',
      '/console/channel',
    );
  });
});

describe('ModelDetailOverview', () => {
  const renderOverview = (over = {}) =>
    render(
      <ModelDetailOverview model={model(over)} vendorMap={vendorMap} t={t} />,
    );

  it('renders nothing without a model', () => {
    const { container } = render(
      <ModelDetailOverview model={null} vendorMap={vendorMap} t={t} />,
    );
    expect(container).toBeEmptyDOMElement();
  });

  it('shows the name copyable and the vendor with its icon', () => {
    const { container } = renderOverview();
    const name = container.querySelector('[data-testid="text"]');
    expect(name.textContent).toBe('gpt-4o');
    expect(name).toHaveAttribute('data-copyable', 'yes');
    expect(container.textContent).toContain('OpenAI');
    expect(
      container.querySelector('[data-testid="lobe-icon"]'),
    ).toHaveAttribute('data-icon', 'OpenAI');
  });

  it('dashes the vendor row when the vendor is unknown', () => {
    const { container } = render(
      <ModelDetailOverview
        model={model({ vendor_id: 999 })}
        vendorMap={vendorMap}
        t={t}
      />,
    );
    expect(container.textContent).toContain('-');
    expect(container.textContent).not.toContain('OpenAI');
  });

  it('labels the match rule, and dashes an unknown rule code', () => {
    expect(renderOverview({ name_rule: 1 }).container.textContent).toContain(
      '前缀',
    );
    const unknown = render(
      <ModelDetailOverview
        model={model({ name_rule: 9 })}
        vendorMap={vendorMap}
        t={t}
      />,
    );
    expect(unknown.container.textContent).not.toContain('前缀');
  });

  it('shows enabled and disabled status distinctly', () => {
    expect(renderOverview({ status: 1 }).container.textContent).toContain(
      '已启用',
    );
    const off = render(
      <ModelDetailOverview
        model={model({ status: 0 })}
        vendorMap={vendorMap}
        t={t}
      />,
    );
    expect(off.container.textContent).toContain('已禁用');
  });

  it('shows both answers for official sync participation', () => {
    expect(
      renderOverview({ sync_official: 1 }).container.textContent,
    ).toContain('是');
    const off = render(
      <ModelDetailOverview
        model={model({ sync_official: 0 })}
        vendorMap={vendorMap}
        t={t}
      />,
    );
    expect(off.container.textContent).toContain('否');
  });

  it('splits tags and lists enabled groups', () => {
    const { container } = renderOverview({
      tags: 'vision,,tools',
      enable_groups: ['default', 'vip'],
    });
    const tags = [...container.querySelectorAll('[data-testid="tag"]')].map(
      (n) => n.textContent,
    );
    expect(tags).toContain('vision');
    expect(tags).toContain('tools');
    expect(tags).toContain('default');
    expect(tags).toContain('vip');
    expect(tags).not.toContain('');
  });

  // quota_type 0 is per-token billing, not "unset": a truthiness test here
  // would silently relabel every per-token model.
  it('labels billing type 0 as 按量计费 and passes unknown codes through', () => {
    const { container } = renderOverview({ quota_types: [0, 1, 5] });
    const labels = [...container.querySelectorAll('[data-testid="tag"]')].map(
      (n) => n.textContent,
    );
    expect(labels).toContain('按量计费');
    expect(labels).toContain('按次计费');
    expect(labels).toContain('5');
  });

  it('lists endpoint keys from a map and from the legacy array form', () => {
    const asMap = renderOverview({
      endpoints: JSON.stringify({ openai: {}, anthropic: {} }),
    });
    expect(asMap.container.textContent).toContain('openai');
    expect(asMap.container.textContent).toContain('anthropic');

    const asArray = render(
      <ModelDetailOverview
        model={model({ endpoints: '["/v1/messages"]' })}
        vendorMap={vendorMap}
        t={t}
      />,
    );
    expect(asArray.container.textContent).toContain('/v1/messages');
  });

  it('survives unparseable endpoint JSON without losing the rest of the pane', () => {
    const { container } = renderOverview({
      endpoints: '{not json',
      description: 'still here',
    });
    expect(container.textContent).toContain('still here');
  });

  it('formats both timestamps through the shared formatter', () => {
    const { container } = renderOverview({
      created_time: 1710000000,
      updated_time: 1720000000,
    });
    expect(container.textContent).toContain('ts(1710000000)');
    expect(container.textContent).toContain('ts(1720000000)');
  });
});

describe('ModelDetailDrawer', () => {
  const drawerProps = (over = {}) => ({
    visible: true,
    model: model(),
    onClose: vi.fn(),
    pricingMap: { 'gpt-4o': { source: 'explicit', ratio: 2.5 } },
    vendorMap,
    manageModel: vi.fn().mockResolvedValue(undefined),
    setEditingModel: vi.fn(),
    setShowEdit: vi.fn(),
    refresh: vi.fn().mockResolvedValue(undefined),
    ...over,
  });

  it('renders nothing when there is no model to show', () => {
    const { container } = render(
      <ModelDetailDrawer {...drawerProps({ model: null })} />,
    );
    expect(container).toBeEmptyDOMElement();
  });

  it('heads the sheet with the model name, its status and its vendor', () => {
    const { getByTestId } = render(<ModelDetailDrawer {...drawerProps()} />);
    const title = getByTestId('sheet-title');
    expect(title.textContent).toContain('gpt-4o');
    expect(title.textContent).toContain('已启用');
    expect(title.textContent).toContain('OpenAI');
    expect(title.querySelector('[data-testid="header-icon"]')).toHaveAttribute(
      'data-icon',
      'OpenAI',
    );
  });

  it('shows a disabled model as disabled and offers to enable it', () => {
    const { getByTestId, queryByTestId } = render(
      <ModelDetailDrawer {...drawerProps({ model: model({ status: 0 }) })} />,
    );
    expect(getByTestId('sheet-title').textContent).toContain('已禁用');
    expect(queryByTestId('btn-启用')).not.toBeNull();
    expect(queryByTestId('btn-禁用')).toBeNull();
  });

  it('opens on the overview pane and switches to pricing on demand', () => {
    const { getByTestId, queryByTestId } = render(
      <ModelDetailDrawer {...drawerProps()} />,
    );
    expect(getByTestId('tabs')).toHaveAttribute('data-active-key', 'overview');
    expect(queryByTestId('pane-overview')).not.toBeNull();
    expect(queryByTestId('pane-pricing')).toBeNull();

    fireEvent.click(getByTestId('tab-pricing'));
    expect(getByTestId('pane-pricing').textContent).toContain('2.500');
  });

  it('hands the same model to the channels pane', () => {
    const { getByTestId } = render(
      <ModelDetailDrawer
        {...drawerProps({
          model: model({ bound_channels: [{ name: 'azure-eu', type: 3 }] }),
        })}
      />,
    );
    fireEvent.click(getByTestId('tab-channels'));
    expect(getByTestId('pane-channels').textContent).toContain('azure-eu');
  });

  it('disables the model it is showing, by id', () => {
    const props = drawerProps();
    const { getByTestId } = render(<ModelDetailDrawer {...props} />);
    fireEvent.click(getByTestId('btn-禁用'));
    expect(props.manageModel).toHaveBeenCalledWith(7, 'disable', props.model);
  });

  it('enables a disabled model, by id', () => {
    const props = drawerProps({ model: model({ id: 7, status: 0 }) });
    const { getByTestId } = render(<ModelDetailDrawer {...props} />);
    fireEvent.click(getByTestId('btn-启用'));
    expect(props.manageModel).toHaveBeenCalledWith(7, 'enable', props.model);
  });

  it('hands the model to the editor and gets out of the way', () => {
    const props = drawerProps();
    const { getByTestId } = render(<ModelDetailDrawer {...props} />);
    fireEvent.click(getByTestId('btn-编辑'));
    expect(props.setEditingModel).toHaveBeenCalledWith(props.model);
    expect(props.setShowEdit).toHaveBeenCalledWith(true);
    expect(props.onClose).toHaveBeenCalled();
  });

  // The negative half that protects the catalogue.
  it('confirms before deleting and deletes nothing on the first click', () => {
    const props = drawerProps();
    const { getByTestId } = render(<ModelDetailDrawer {...props} />);
    fireEvent.click(getByTestId('btn-删除'));
    expect(props.manageModel).not.toHaveBeenCalled();
    expect(props.onClose).not.toHaveBeenCalled();
    expect(semiModalConfirm).toHaveBeenCalledTimes(1);
    expect(semiModalConfirm.mock.calls[0][0].title).toBe(
      '确定是否要删除此模型？',
    );
  });

  it('deletes, refreshes and closes once the confirmation is accepted', async () => {
    const props = drawerProps();
    const { getByTestId } = render(<ModelDetailDrawer {...props} />);
    fireEvent.click(getByTestId('btn-删除'));
    await semiModalConfirm.mock.calls[0][0].onOk();
    await waitFor(() =>
      expect(props.manageModel).toHaveBeenCalledWith(7, 'delete', props.model),
    );
    expect(props.refresh).toHaveBeenCalled();
    expect(props.onClose).toHaveBeenCalled();
  });

  it('closes on cancel without touching the model', () => {
    const props = drawerProps();
    const { getByTestId } = render(<ModelDetailDrawer {...props} />);
    fireEvent.click(getByTestId('sheet-cancel'));
    expect(props.onClose).toHaveBeenCalled();
    expect(props.manageModel).not.toHaveBeenCalled();
  });
});
