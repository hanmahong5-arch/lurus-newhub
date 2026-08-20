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

// The channels page shell. Two things here are worth guarding:
//   * the GLOBAL pass-through banner. When request pass-through is on,
//     model redirect / param override / channel adaptation all stop working
//     silently; this banner is the only warning an operator gets. It is
//     asserted with a positive AND a negative case, because "warns when
//     pass-through is on" and "always warns" look identical otherwise.
//   * the modals that are wired by explicit props rather than {...spread} —
//     those are the ones a rename can silently disconnect.

import React from 'react';
import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen } from '@testing-library/react';

const H = vi.hoisted(() => ({ data: { current: null } }));

vi.mock('../../../hooks/channels/useChannelsData', () => ({
  useChannelsData: () => H.data.current,
}));

vi.mock('../../../hooks/common/useIsMobile', () => ({
  useIsMobile: () => false,
}));

// Banner renders a MARKER carrying its description, so the warning's presence
// AND its text are both observable. A `() => null` stub here would let the
// entire warning be deleted without a test noticing.
vi.mock('@douyinfe/semi-ui', () => ({
  Banner: ({ type, description }) =>
    React.createElement(
      'div',
      { 'data-testid': 'global-banner', 'data-type': type },
      description,
    ),
}));

vi.mock('@douyinfe/semi-icons', () => ({
  IconAlertTriangle: () =>
    React.createElement('i', { 'data-testid': 'banner-icon' }),
}));

vi.mock('../../common/ui/CardPro', () => ({
  default: ({ tabsArea, actionsArea, searchArea, paginationArea, children }) =>
    React.createElement(
      'div',
      { 'data-testid': 'card-pro' },
      React.createElement('div', { 'data-testid': 'area-tabs' }, tabsArea),
      React.createElement(
        'div',
        { 'data-testid': 'area-actions' },
        actionsArea,
      ),
      React.createElement('div', { 'data-testid': 'area-search' }, searchArea),
      // createCardProPagination returns a config object, not an element, so
      // surface it as JSON rather than trying to render it.
      React.createElement('div', {
        'data-testid': 'area-pagination',
        'data-config': JSON.stringify(
          Object.fromEntries(
            Object.entries(paginationArea ?? {}).filter(
              ([, v]) => typeof v !== 'function',
            ),
          ),
        ),
      }),
      children,
    ),
}));

// Each child renders a MARKER carrying its non-function props, so the wiring
// is observable. `stub` is redefined inside every factory because vi.mock is
// hoisted above any top-level binding.
// JSX here compiles to the automatic runtime, which does not need React in
// scope — so this works inside a hoisted factory.
const S = vi.hoisted(() => ({
  make: (testid) => ({
    default: (props) => (
      <div
        data-testid={testid}
        data-props={JSON.stringify(
          Object.fromEntries(
            Object.entries(props).filter(([, v]) => typeof v !== 'function'),
          ),
        )}
      />
    ),
  }),
}));

vi.mock('./ChannelsTable', () => S.make('channels-table'));
vi.mock('./ChannelsActions', () => S.make('channels-actions'));
vi.mock('./ChannelsActionRail', () => S.make('channels-rail'));
vi.mock('./ChannelsFilters', () => S.make('channels-filters'));
vi.mock('./ChannelsTabs', () => S.make('channels-tabs'));
vi.mock('./modals/BatchTagModal', () => S.make('batch-tag-modal'));
vi.mock('./modals/ModelTestModal', () => S.make('model-test-modal'));
vi.mock('./modals/ColumnSelectorModal', () => S.make('column-selector-modal'));
vi.mock('./modals/EditChannelModal', () => S.make('edit-channel-modal'));
vi.mock('./modals/EditTagModal', () => S.make('edit-tag-modal'));
vi.mock('./modals/MultiKeyManageModal', () => S.make('multi-key-modal'));

vi.mock('../../../helpers/utils', () => ({
  createCardProPagination: (cfg) => ({ marker: 'pagination', ...cfg }),
}));

import ChannelsPage from './index';

const t = (k) => k;

const makeData = (over = {}) => ({
  t,
  globalPassThroughEnabled: false,
  activePage: 2,
  pageSize: 20,
  channelCount: 41,
  handlePageChange: vi.fn(),
  handlePageSizeChange: vi.fn(),
  showEditTag: false,
  editingTag: null,
  setShowEditTag: vi.fn(),
  refresh: vi.fn(),
  showEdit: false,
  closeEdit: vi.fn(),
  editingChannel: { id: 5 },
  showMultiKeyManageModal: false,
  setShowMultiKeyManageModal: vi.fn(),
  currentMultiKeyChannel: { id: 9 },
  ...over,
});

const renderPage = (over = {}) => {
  H.data.current = makeData(over);
  render(<ChannelsPage />);
  return H.data.current;
};

const propsOf = (testid) =>
  JSON.parse(screen.getByTestId(testid).dataset.props);

beforeEach(() => vi.clearAllMocks());

describe('global pass-through warning', () => {
  it('warns, in the strongest terms, when global pass-through is enabled', () => {
    renderPage({ globalPassThroughEnabled: true });
    const banner = screen.getByTestId('global-banner');
    expect(banner.dataset.type).toBe('warning');
    // The text is the warning. A banner that renders empty is no warning.
    expect(banner.textContent).toContain('已开启全局请求透传');
    expect(banner.textContent).toContain('模型重定向');
  });

  it('shows NO banner when global pass-through is off', () => {
    renderPage({ globalPassThroughEnabled: false });
    expect(screen.queryByTestId('global-banner')).toBeNull();
  });

  it('shows no banner when the flag is absent entirely', () => {
    renderPage({ globalPassThroughEnabled: undefined });
    expect(screen.queryByTestId('global-banner')).toBeNull();
  });
});

describe('modal wiring', () => {
  it('drives the tag editor from its own visibility flag and tag', () => {
    renderPage({ showEditTag: true, editingTag: 'eu-west' });
    expect(propsOf('edit-tag-modal')).toMatchObject({
      visible: true,
      tag: 'eu-west',
    });
  });

  it('keeps the tag editor closed when its flag is off', () => {
    renderPage({ showEditTag: false });
    expect(propsOf('edit-tag-modal').visible).toBe(false);
  });

  it('hands the channel editor the channel being edited', () => {
    renderPage({ showEdit: true, editingChannel: { id: 5, name: 'azure' } });
    expect(propsOf('edit-channel-modal')).toMatchObject({
      visible: true,
      editingChannel: { id: 5, name: 'azure' },
    });
  });

  it('hands the multi-key manager the channel whose keys are being managed', () => {
    renderPage({
      showMultiKeyManageModal: true,
      currentMultiKeyChannel: { id: 9, name: 'pool' },
    });
    expect(propsOf('multi-key-modal')).toMatchObject({
      visible: true,
      channel: { id: 9, name: 'pool' },
    });
  });

  it('never leaves the multi-key manager open with no channel selected', () => {
    renderPage({
      showMultiKeyManageModal: false,
      currentMultiKeyChannel: null,
    });
    const p = propsOf('multi-key-modal');
    expect(p.visible === true && p.channel === null).toBe(false);
  });
});

describe('layout composition', () => {
  it('mounts the rail, tabs, actions, filters and table exactly once each', () => {
    renderPage();
    for (const id of [
      'channels-rail',
      'channels-tabs',
      'channels-actions',
      'channels-filters',
      'channels-table',
    ]) {
      expect(screen.getAllByTestId(id)).toHaveLength(1);
    }
  });

  it('puts each region in its own slot rather than stacking them', () => {
    renderPage();
    expect(
      screen
        .getByTestId('area-tabs')
        .querySelector('[data-testid="channels-tabs"]'),
    ).not.toBeNull();
    expect(
      screen
        .getByTestId('area-search')
        .querySelector('[data-testid="channels-filters"]'),
    ).not.toBeNull();
    expect(
      screen
        .getByTestId('area-actions')
        .querySelector('[data-testid="channels-actions"]'),
    ).not.toBeNull();
  });

  it('builds pagination from the live page, size and total', () => {
    renderPage({ activePage: 2, pageSize: 20, channelCount: 41 });
    // The page control is how an operator reaches channels 21-41; a stale
    // total here strands rows out of reach.
    const cfg = JSON.parse(
      screen.getByTestId('area-pagination').dataset.config,
    );
    expect(cfg).toMatchObject({
      marker: 'pagination',
      currentPage: 2,
      pageSize: 20,
      total: 41,
      isMobile: false,
    });
  });
});
